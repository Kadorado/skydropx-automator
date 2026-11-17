package customers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoRepo keeps customers in a MongoDB collection.
type mongoRepo struct {
	coll *mongo.Collection
	meta *mongo.Collection
}

// OpenMongo connects to MongoDB (e.g. Atlas, with a mongodb+srv:// URI), checks the connection and
// prepares the "customers" collection of the given database. Call the returned close function on shutdown.
func OpenMongo(ctx context.Context, uri, database string) (*Store, func(context.Context) error, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetAppName("skydropx-automator"))
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to MongoDB: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("MongoDB doesn't answer (check MONGODB_URI and Network Access in Atlas): %w", err)
	}
	coll := client.Database(database).Collection("customers")
	// One customer per ID document, enforced by MongoDB (customers without a document are left out).
	// The phone isn't unique: relatives with different documents may share one.
	_, err = coll.Indexes().CreateMany(pingCtx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "document_id", Value: 1}}, Options: options.Index().SetName("document_id_unique").SetUnique(true).
			SetPartialFilterExpression(bson.D{{Key: "document_id", Value: bson.D{{Key: "$gt", Value: ""}}}})},
		{Keys: bson.D{{Key: "phone", Value: 1}}},
	})
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("creating MongoDB indexes: %w", err)
	}
	meta := client.Database(database).Collection("meta")
	since, err := startedAt(pingCtx, meta)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("reading when the customer list started: %w", err)
	}
	return &Store{repo: &mongoRepo{coll: coll, meta: meta}, now: time.Now, since: since}, client.Disconnect, nil
}

// startedAt returns when the bot first connected to this database, recording it the first time. From
// then on every order is recorded by the bot, so the migration from Skydropx stops there.
func startedAt(ctx context.Context, meta *mongo.Collection) (time.Time, error) {
	var doc struct {
		StartedAt time.Time `bson:"started_at"`
	}
	err := meta.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: "customers"}},
		bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "started_at", Value: time.Now()}}}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&doc)
	return doc.StartedAt, err
}

// findOne decodes the first match of filter (nil if there is none).
func (m *mongoRepo) findOne(ctx context.Context, filter bson.D) (*Customer, error) {
	var c Customer
	err := m.coll.FindOne(ctx, filter).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// findByKeys matches by document first. By phone it only matches customers without a document, or any
// customer when no document was given, so two people sharing a phone stay apart.
func (m *mongoRepo) findByKeys(ctx context.Context, documentID, phone string) (*Customer, error) {
	if documentID != "" {
		c, err := m.findOne(ctx, bson.D{{Key: "document_id", Value: documentID}})
		if c != nil || err != nil {
			return c, err
		}
	}
	if phone == "" {
		return nil, nil
	}
	filter := bson.D{{Key: "phone", Value: phone}}
	if documentID != "" {
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "document_id", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "document_id", Value: ""}},
		}})
	}
	return m.findOne(ctx, filter)
}

// addOrder applies merge in a single atomic upsert: an update pipeline computes every field from the
// stored document, so two orders of the same customer at once both end up in it.
func (m *mongoRepo) addOrder(ctx context.Context, id string, in Customer, o Order, historical bool, now time.Time) (Customer, error) {
	literal := func(v any) bson.D { return bson.D{{Key: "$literal", Value: v}} }
	// keepIfSet keeps the stored value of field if it isn't empty, else uses v.
	keepIfSet := func(field string, v any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$" + field, ""}}}, ""}}}, "$" + field, literal(v),
		}}}
	}
	set := bson.D{}
	for _, f := range contactFields(&Customer{}, in) {
		switch {
		case f.value == "":
		case historical:
			set = append(set, bson.E{Key: f.bson, Value: keepIfSet(f.bson, f.value)})
		default:
			set = append(set, bson.E{Key: f.bson, Value: literal(f.value)})
		}
	}
	if src := normalizeSource(in.Source); src != "" {
		set = append(set, bson.E{Key: "source", Value: keepIfSet("source", src)})
	}
	consent := bson.D{{Key: "$ifNull", Value: bson.A{"$consent", literal(Consent{})}}}
	if in.Consent.Given {
		consent = bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$consent.given", true}}}, "$consent", literal(in.Consent),
		}}}
	}
	stored := bson.D{{Key: "$ifNull", Value: bson.A{"$orders", bson.A{}}}}
	var orders any = bson.D{{Key: "$concatArrays", Value: bson.A{stored, literal(bson.A{o})}}}
	if historical { // skip it if an order with the same date and time is already there
		orders = bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$in", Value: bson.A{o.Date, bson.D{{Key: "$ifNull", Value: bson.A{"$orders.date", bson.A{}}}}}}}, stored, orders,
		}}}
	}
	set = append(set,
		bson.E{Key: "consent", Value: consent},
		bson.E{Key: "orders", Value: orders},
		bson.E{Key: "first_order_at", Value: bson.D{{Key: "$min", Value: bson.A{"$first_order_at", o.Date}}}},
		bson.E{Key: "last_order_at", Value: bson.D{{Key: "$max", Value: bson.A{"$last_order_at", o.Date}}}},
		bson.E{Key: "updated_at", Value: now},
	)
	var c Customer
	err := m.coll.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: id}}, mongo.Pipeline{{{Key: "$set", Value: set}}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&c)
	if mongo.IsDuplicateKeyError(err) {
		return Customer{}, errDuplicate
	}
	return c, err
}

// updateOne applies update to the customer with that ID and returns it as stored.
func (m *mongoRepo) updateOne(ctx context.Context, id string, update bson.D) (Customer, error) {
	var c Customer
	err := m.coll.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: id}}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

func (m *mongoRepo) setConsent(ctx context.Context, id string, consent Consent, now time.Time) (Customer, error) {
	return m.updateOne(ctx, id, bson.D{
		{Key: "$set", Value: bson.D{{Key: "consent", Value: consent}, {Key: "updated_at", Value: now}}},
		{Key: "$unset", Value: bson.D{{Key: "opt_out_at", Value: ""}}},
	})
}

func (m *mongoRepo) setOptOut(ctx context.Context, id string, now time.Time) (Customer, error) {
	return m.updateOne(ctx, id, bson.D{{Key: "$set", Value: bson.D{{Key: "opt_out_at", Value: now}, {Key: "updated_at", Value: now}}}})
}

func (m *mongoRepo) insertIfMissing(ctx context.Context, c Customer) (bool, error) {
	_, err := m.coll.InsertOne(ctx, c)
	if !mongo.IsDuplicateKeyError(err) {
		return err == nil, err
	}
	// Either the customer exists (same ID: leave it) or its document belongs to another customer.
	n, cerr := m.coll.CountDocuments(ctx, bson.D{{Key: "_id", Value: c.ID}})
	if cerr != nil {
		return false, cerr
	}
	if n > 0 {
		return false, nil
	}
	return false, errDuplicate
}

func (m *mongoRepo) restoreSince(ctx context.Context, t time.Time) (time.Time, error) {
	var doc struct {
		StartedAt time.Time `bson:"started_at"`
	}
	err := m.meta.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: "customers"}},
		bson.D{{Key: "$min", Value: bson.D{{Key: "started_at", Value: t}}}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&doc)
	return doc.StartedAt, err
}

func (m *mongoRepo) all(ctx context.Context) ([]Customer, error) {
	cur, err := m.coll.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	var out []Customer
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
