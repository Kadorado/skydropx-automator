package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"skydropx-automator/internal/config"
	"skydropx-automator/internal/domain"
	"skydropx-automator/internal/settings"
	"skydropx-automator/internal/skydropx"
	"skydropx-automator/internal/telegram"
)

// fakeAPIs simulates Telegram and Skydropx in a single server and records what the bot sends.
type fakeAPIs struct {
	mu           sync.Mutex
	edits        []string // editMessageText texts
	buttons      []string // button data of the last edited message
	shipments    int      // times a shipment was created
	document     []byte   // PDF sent with sendDocument
	alias        []string // aliases of the saved addresses
	balance      float64  // balance returned by /finance/credits
	noBalance    bool     // if true, creating the shipment answers "Fondos insuficientes"
	caption      string
	srvURL       string
	messages     []string          // sendMessage texts
	fileData     []byte            // file that getFile + download return
	shipmentList string            // body of GET /api/v1/shipments (one page); empty = 5 labels of today
	tracking     map[string]string // tracking number -> last status of its tracking
}

func (f *fakeAPIs) handler() http.Handler {
	mux := http.NewServeMux()
	// Telegram
	mux.HandleFunc("POST /botTOKEN/editMessageText", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text   string `json:"text"`
			Markup struct {
				Keyboard [][]telegram.Button `json:"inline_keyboard"`
			} `json:"reply_markup"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.edits = append(f.edits, body.Text)
		f.buttons = nil
		for _, row := range body.Markup.Keyboard {
			for _, b := range row {
				f.buttons = append(f.buttons, b.Data)
			}
		}
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	mux.HandleFunc("POST /botTOKEN/answerCallbackQuery", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	})
	mux.HandleFunc("POST /botTOKEN/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.messages = append(f.messages, body.Text)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	})
	mux.HandleFunc("GET /api/v1/shipments/tracking", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status := f.tracking[r.URL.Query().Get("tracking_number")]
		f.mu.Unlock()
		events := `{"attributes":{"description":"Guía creada","status":"created","date":"2026-11-13T18:10:00-05:00"}}`
		if status != "created" && status != "" {
			events += `,{"attributes":{"description":"Recibido en oficina","status":"` + status + `","date":"2026-11-14T09:00:00-05:00"}}`
		}
		_, _ = io.WriteString(w, `{"data":[`+events+`]}`)
	})
	mux.HandleFunc("POST /botTOKEN/getFile", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_path":"documents/file.json"}}`)
	})
	mux.HandleFunc("GET /file/botTOKEN/documents/file.json", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_, _ = w.Write(f.fileData)
	})
	mux.HandleFunc("POST /botTOKEN/sendDocument", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			file, _, _ := r.FormFile("document")
			data, _ := io.ReadAll(file)
			f.mu.Lock()
			f.document = data
			f.caption = r.FormValue("caption")
			f.mu.Unlock()
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	// Skydropx
	mux.HandleFunc("POST /api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":7200}`)
	})
	mux.HandleFunc("POST /api/v1/quotations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"q1"}`)
	})
	mux.HandleFunc("GET /api/v1/quotations/q1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"is_completed":true,"rates":[{"id":"r1","success":true,"provider_name":"interrapidisimo","total":"14500","days":2}]}`)
	})
	mux.HandleFunc("GET /api/v1/finance/credits", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		fmt.Fprintf(w, `{"data":{"balance":%v,"currency":"COP"},"meta":{"country_code":"CO"}}`, f.balance)
	})
	mux.HandleFunc("POST /api/v1/shipments/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.noBalance {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"errors":"Fondos insuficientes","error_code":"INSUFFICIENT_CREDITS"}`)
			return
		}
		f.shipments++
		f.balance -= 14500
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":{"id":"shp-1"}}`)
	})
	mux.HandleFunc("GET /api/v1/address_templates", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[],"meta":{"next_page":null}}`)
	})
	mux.HandleFunc("POST /api/v1/address_templates/", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Template struct {
				Alias string `json:"alias_name"`
			} `json:"address_template"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.alias = append(f.alias, body.Template.Alias)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"id":"tpl-1"}}`)
	})
	mux.HandleFunc("GET /api/v1/shipments/shp-1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"id":"shp-1","attributes":{}}}`) // the label isn't ready yet
	})
	mux.HandleFunc("GET /api/v1/shipments", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		custom := f.shipmentList
		f.mu.Unlock()
		if custom != "" {
			_, _ = io.WriteString(w, custom)
			return
		}
		today := time.Now().In(colombiaTZ).Format(time.RFC3339)
		var data, inc []string
		for i := 1; i <= 5; i++ {
			data = append(data, fmt.Sprintf(`{"id":"s%d","attributes":{"created_at":%q,"master_tracking_number":"G%d"},"relationships":{"packages":{"data":[{"id":"p%d"}]}}}`, i, today, i, i))
			inc = append(inc, fmt.Sprintf(`{"id":"p%d","type":"package","attributes":{"label_url":"%s/labels/%d.pdf"}}`, i, f.srvURL, i))
		}
		fmt.Fprintf(w, `{"data":[%s],"included":[%s],"meta":{"total_pages":1}}`, strings.Join(data, ","), strings.Join(inc, ","))
	})
	mux.HandleFunc("GET /labels/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fakeLabelPDF())
	})
	return mux
}

func newTestBot(t *testing.T) (*Bot, *fakeAPIs) {
	f := &fakeAPIs{balance: 100000}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	f.srvURL = srv.URL
	sky := skydropx.NewClient(config.Config{
		SkydropxMode: "live", SkydropxBaseURL: srv.URL, SkydropxClientID: "id", SkydropxClientSecret: "secret",
		OriginAddressTemplateID: "tpl", OriginCountry: "CO", OriginEmail: "tienda@example.com",
		DefaultWeightKg: 1, DefaultLengthCm: 10, DefaultWidthCm: 10, DefaultHeightCm: 15, DefaultContent: "datafono",
	})
	b, err := New(Options{
		Token: "TOKEN", APIBase: srv.URL, AdminChatIDs: []int64{-100},
		StatePath: filepath.Join(t.TempDir(), "state.json"), CheckEvery: time.Hour,
	}, sky)
	if err != nil {
		t.Fatal(err)
	}
	return b, f
}

func callback(data string) *telegram.CallbackQuery {
	return &telegram.CallbackQuery{ID: "cb", Data: data, Message: &telegram.Message{MessageID: 7, Chat: telegram.Chat{ID: -100}}}
}

func TestGenerateAndConfirmLabel(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	data := domain.ShipmentData{Name: "Cliente de Prueba", Phone: "3000000000", Department: "Antioquia",
		City: "Medellín", Address: "Calle 1", Quantity: 1, UnitPrice: 60000, PostalCode: "050001"}
	if err := b.st.add(Pending{OrderID: "orden-1", ChatID: 1, Sender: "Vendedor", Data: &data, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	b.handleCallback(ctx, callback("g:orden-1"))
	last := f.edits[len(f.edits)-1]
	if !strings.Contains(last, "cobra <b>$74.500</b> al recibir (producto $60.000 + envío $14.500)") {
		t.Fatalf("debe mostrar el cobro contra entrega con el envío incluido, llegó: %s", last)
	}
	if !strings.Contains(last, "Saldo actual: $100.000") {
		t.Fatalf("la cotización debe mostrar el saldo, llegó: %s", last)
	}
	if !strings.Contains(last, "$14.500") || !strings.Contains(last, "Sin Protección Plus") {
		t.Fatalf("la cotización debería mostrar precio y protección, llegó: %s", last)
	}
	if strings.Join(f.buttons, ",") != "c:orden-1,x:orden-1" {
		t.Fatalf("botones = %v, se esperaba Confirmar y Cancelar", f.buttons)
	}

	b.handleCallback(ctx, callback("c:orden-1"))
	b.handleCallback(ctx, callback("c:orden-1")) // double tap
	if f.shipments != 1 {
		t.Fatalf("se crearon %d envíos, debe ser 1 aunque se toque dos veces", f.shipments)
	}
	if last := f.edits[len(f.edits)-1]; !strings.Contains(last, "Guía solicitada") || !strings.Contains(last, "Dirección guardada") {
		t.Fatalf("debería confirmar la guía y la dirección guardada, llegó: %s", last)
	}
	if last := f.edits[len(f.edits)-1]; !strings.Contains(last, "Saldo restante: <b>$85.500</b>") {
		t.Fatalf("después de la guía debe mostrar el saldo restante, llegó: %s", last)
	}
	if strings.Join(f.alias, ",") != "Cliente de Prueba" {
		t.Fatalf("la dirección debe guardarse una vez con el nombre del cliente, alias = %v", f.alias)
	}
}

func TestCancelRestoresGenerateButton(t *testing.T) {
	b, f := newTestBot(t)
	ctx := context.Background()
	data := domain.ShipmentData{Name: "Cliente", City: "Medellín", Department: "Antioquia", PostalCode: "050001", UnitPrice: 60000}
	_ = b.st.add(Pending{OrderID: "orden-2", Data: &data})

	b.handleCallback(ctx, callback("g:orden-2"))
	b.handleCallback(ctx, callback("x:orden-2"))
	if strings.Join(f.buttons, ",") != "g:orden-2" {
		t.Fatalf("después de cancelar debe quedar el botón de generar, quedó %v", f.buttons)
	}
	if p, _ := b.st.getPending("orden-2"); p.Quote != nil {
		t.Fatal("cancelar debe borrar la cotización")
	}
}

func TestPrintTodaySendsSheets(t *testing.T) {
	b, f := newTestBot(t)
	b.printLabels(context.Background(), -100, 1)
	if len(f.document) == 0 {
		t.Fatal("no se envió el PDF")
	}
	if !bytes.HasPrefix(f.document, []byte("%PDF")) {
		t.Fatal("lo enviado no es un PDF")
	}
	if !strings.Contains(f.caption, "5 guía(s) de hoy") || !strings.Contains(f.caption, "2 hoja(s)") {
		t.Fatalf("5 guías deben ir en 2 hojas, caption: %s", f.caption)
	}
}

// fakeLabelPDF builds a minimal PDF with the Interrapidísimo label format (landscape A4 rotated 90°).
func fakeLabelPDF() []byte {
	content := "BT /F1 24 Tf 40 300 Td (GUIA DE PRUEBA) Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 842 595] /Rotate 90 /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return buf.Bytes()
}

func TestButtonsOnlyInOpsGroup(t *testing.T) {
	b, f := newTestBot(t)
	b.opts.OpsChatIDs = []int64{-200} // group -100 (seller) no longer handles labels
	data := domain.ShipmentData{Name: "Cliente", City: "Medellín", Department: "Antioquia", PostalCode: "050001", UnitPrice: 60000}
	_ = b.st.add(Pending{OrderID: "orden-3", Data: &data})

	b.handleCallback(context.Background(), callback("g:orden-3")) // tapped from -100
	if len(f.edits) != 0 {
		t.Fatalf("un botón del grupo del vendedor no debe cotizar, se editó: %v", f.edits)
	}
	if !b.isOps(-200) || b.isOps(-100) {
		t.Fatal("solo -200 debe ser el grupo de gestión")
	}
}

func TestDaysToPrint(t *testing.T) {
	cases := map[string]int{
		"/imprimir":          1,
		"/imprimir 3":        3,
		"/imprimir@mi_bot 5": 5,
		"/imprimir 40":       maxPrintDays,
		"/imprimir 0":        1,
		"/imprimir hoy":      1,
	}
	for msgText, want := range cases {
		if got := daysToPrint(msgText); got != want {
			t.Errorf("diasAImprimir(%q) = %d, want %d", msgText, got, want)
		}
	}
}

func TestNewOrderReachesBothGroupsAndButtonOnlyOps(t *testing.T) {
	f := &fakeAPIs{}
	var mu sync.Mutex
	sent := map[int64]string{} // chat -> message buttons
	mux := http.NewServeMux()
	mux.HandleFunc("POST /botTOKEN/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID int64 `json:"chat_id"`
			Markup struct {
				Keyboard [][]telegram.Button `json:"inline_keyboard"`
			} `json:"reply_markup"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var payloadData []string
		for _, row := range body.Markup.Keyboard {
			for _, b := range row {
				payloadData = append(payloadData, b.Data)
			}
		}
		mu.Lock()
		sent[body.ChatID] = strings.Join(payloadData, ",")
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	})
	mux.Handle("/", f.handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b, err := New(Options{
		Token: "TOKEN", APIBase: srv.URL, AdminChatIDs: []int64{-100}, OpsChatIDs: []int64{-200},
		StatePath: filepath.Join(t.TempDir(), "state.json"), CheckEvery: time.Hour,
	}, skydropx.NewClient(config.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	b.notifyAdmins(context.Background(), "Vendedor", domain.ShipmentData{Name: "Cliente"}, "orden-9", false, "", "")

	if got, ok := sent[-200]; !ok || got != "g:orden-9" {
		t.Fatalf("tu grupo debe recibir la orden con el botón, recibió %q (ok=%v)", got, ok)
	}
	if got, ok := sent[-100]; !ok || got != "" {
		t.Fatalf("el grupo del vendedor debe recibir la orden sin botones, recibió %q (ok=%v)", got, ok)
	}
}

func TestInsufficientBalance(t *testing.T) {
	b, f := newTestBot(t)
	f.balance = 5000
	ctx := context.Background()
	data := domain.ShipmentData{Name: "Cliente", City: "Medellín", Department: "Antioquia", PostalCode: "050001", UnitPrice: 60000}
	_ = b.st.add(Pending{OrderID: "orden-4", Data: &data})

	b.handleCallback(ctx, callback("g:orden-4"))
	if last := f.edits[len(f.edits)-1]; !strings.Contains(last, "Saldo insuficiente:</b> tienes $5.000 y la guía cuesta $14.500") {
		t.Fatalf("debe avisar antes de confirmar que no alcanza, llegó: %s", last)
	}

	f.noBalance = true
	b.handleCallback(ctx, callback("c:orden-4"))
	last := f.edits[len(f.edits)-1]
	if !strings.Contains(last, "saldo insuficiente en Skydropx") || strings.Join(f.buttons, ",") != "g:orden-4" {
		t.Fatalf("debe explicar el rechazo por saldo y ofrecer reintentar, llegó: %s (botones %v)", last, f.buttons)
	}
	if p, _ := b.st.getPending("orden-4"); p.LabelRequested {
		t.Fatal("si Skydropx rechaza, la orden debe poder reintentarse")
	}
}

func TestNoticesAndCommissionOnlyInOpsGroup(t *testing.T) {
	f := &fakeAPIs{balance: 100000}
	var mu sync.Mutex
	received := map[int64][]string{} // chat -> texts
	mux := http.NewServeMux()
	mux.HandleFunc("POST /botTOKEN/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received[body.ChatID] = append(received[body.ChatID], body.Text)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	})
	mux.Handle("/", f.handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f.srvURL = srv.URL

	appSettings, err := settings.Load(filepath.Join(t.TempDir(), "settings.json"), 15000)
	if err != nil {
		t.Fatal(err)
	}
	sky := skydropx.NewClient(config.Config{
		SkydropxMode: "live", SkydropxBaseURL: srv.URL, SkydropxClientID: "id", SkydropxClientSecret: "secret",
	})
	b, err := New(Options{
		Token: "TOKEN", APIBase: srv.URL, AllowedChatIDs: []int64{-100}, AdminChatIDs: []int64{-100}, OpsChatIDs: []int64{-200},
		StatePath: filepath.Join(t.TempDir(), "state.json"), CheckEvery: time.Hour, Settings: appSettings,
	}, sky)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// The seller can't change the commission.
	b.handle(ctx, -100, "Vendedor", "/comision 30000")
	if got := appSettings.CurrentCommission(); got != 15000 {
		t.Fatalf("el vendedor cambió la comisión a %v", got)
	}
	// From your group it can.
	b.handle(ctx, -200, "Tú", "/comision 12000")
	if got := appSettings.CurrentCommission(); got != 12000 {
		t.Fatalf("la comisión debía quedar en 12000, quedó en %v", got)
	}

	// The payment and the delivery problem reach both groups (to the seller, the payment without the balance).
	mu.Lock()
	received = map[int64][]string{}
	mu.Unlock()
	b.deliveryNotice("s1", "G1", "collected")
	b.incidentNotice("s2", "delivery_attempt", "G2", "Destinatario ausente", "")
	mu.Lock()
	defer mu.Unlock()
	if len(received[-100]) != 2 || !strings.Contains(received[-100][0], "Pagaron") || strings.Contains(received[-100][0], "saldo disponible") ||
		!strings.Contains(received[-100][1], "No pudieron entregar") {
		t.Fatalf("el grupo del vendedor debía recibir el pago (sin lo del saldo) y el problema de entrega: %q", received[-100])
	}
	if len(received[-200]) != 2 || !strings.Contains(received[-200][0], "Pagaron") || !strings.Contains(received[-200][1], "No pudieron entregar") {
		t.Fatalf("tu grupo debía recibir el pago y el problema de entrega: %q", received[-200])
	}
}

func TestHelpInUnauthorizedSellerGroup(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /botTOKEN/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		texts = append(texts, body.Text)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b, err := New(Options{
		Token: "TOKEN", APIBase: srv.URL, AllowedChatIDs: []int64{111}, AdminChatIDs: []int64{-5031891945}, OpsChatIDs: []int64{-200},
		StatePath: filepath.Join(t.TempDir(), "state.json"), CheckEvery: time.Hour,
	}, skydropx.NewClient(config.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	b.handle(context.Background(), -5031891945, "Vendedor", "/ayuda")
	b.handle(context.Background(), -999, "Otro", "/ayuda")
	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 2 || !strings.Contains(texts[0], "GRUPO DE GUÍAS") {
		t.Fatalf("el grupo del vendedor debía recibir la ayuda: %q", texts)
	}
	if !strings.Contains(texts[1], "No estás autorizado") {
		t.Fatalf("un chat desconocido no debe recibir la ayuda: %q", texts[1])
	}
}
