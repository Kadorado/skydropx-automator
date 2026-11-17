(() => {
  const $ = (id) => document.getElementById(id);
  const TZ = "America/Bogota";

  const state = { from: "", to: "", preset: "mes", loaded: false, loading: false };

  const cop = new Intl.NumberFormat("es-CO", { style: "currency", currency: "COP", maximumFractionDigits: 0 });
  const money = (v) => cop.format(Math.round(Number(v) || 0)).replace(/\s/g, " ");
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

  function escapeHtml(text) {
    return String(text ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  // ---------- Dates (Colombia time) ----------

  function today() {
    return new Date().toLocaleDateString("en-CA", { timeZone: TZ });
  }

  function monthBounds(ym) {
    const [y, m] = ym.split("-").map(Number);
    const last = new Date(Date.UTC(y, m, 0)).getUTCDate();
    const fromDate = `${ym}-01`;
    let toDate = `${ym}-${String(last).padStart(2, "0")}`;
    if (toDate > today()) toDate = today();
    return { from: fromDate, to: toDate };
  }

  function prevMonth(ym) {
    const [y, m] = ym.split("-").map(Number);
    const d = new Date(Date.UTC(y, m - 2, 1));
    return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}`;
  }

  function fmtDay(ymd) {
    if (!ymd) return "";
    const [y, m, d] = ymd.split("-").map(Number);
    return new Date(Date.UTC(y, m - 1, d, 12)).toLocaleDateString("es-CO", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
  }

  function fmtDateTime(iso) {
    const d = new Date(iso);
    if (isNaN(d)) return iso || "";
    return d.toLocaleString("es-CO", { timeZone: TZ, day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
  }

  function periodLabel() {
    if (state.preset === "todo") return `Acumulado hasta el ${fmtDay(state.to)}`;
    if (state.from === state.to) return state.from === today() ? "Hoy" : fmtDay(state.from);
    return `Del ${fmtDay(state.from)} al ${fmtDay(state.to)}`;
  }

  function setPreset(preset) {
    const t = today();
    const ym = t.slice(0, 7);
    state.preset = preset;
    if (preset === "hoy") Object.assign(state, { from: t, to: t });
    if (preset === "mes") Object.assign(state, monthBounds(ym));
    if (preset === "mes-anterior") Object.assign(state, monthBounds(prevMonth(ym)));
    if (preset === "todo") Object.assign(state, { from: "", to: t });
    syncControls();
    load();
  }

  function syncControls() {
    document.querySelectorAll("[data-fin-preset]").forEach((b) => {
      b.setAttribute("aria-pressed", String(b.dataset.finPreset === state.preset));
    });
    $("fin-desde").value = state.from;
    $("fin-hasta").value = state.to;
    const sameMonth = state.from && state.from.slice(0, 7) === state.to.slice(0, 7) && state.from.endsWith("-01");
    $("fin-mes").value = sameMonth ? state.from.slice(0, 7) : "";
    $("fin-periodo").textContent = periodLabel();
  }

  // ---------- Loading ----------

  async function load() {
    state.loading = true;
    renderLoading();
    const q = new URLSearchParams();
    if (state.from) q.set("desde", state.from);
    if (state.to) q.set("hasta", state.to);
    try {
      const res = await fetch(`/api/finanzas?${q}`);
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudieron cargar las finanzas");
      state.loaded = true;
      render(body);
    } catch (err) {
      renderError(err.message);
      window.toast(err.message, "error");
    } finally {
      state.loading = false;
    }
  }

  // ---------- Render ----------

  const LOADING_IDS = [
    "fin-saldo", "fin-com-pagar", "fin-com-pend", "fin-com-total",
    "fin-cobrado", "fin-porcobrar", "fin-fallido", "fin-envios",
  ];

  function renderLoading() {
    LOADING_IDS.forEach((id) => {
      $(id).innerHTML = `<span class="skeleton inline-block h-7 w-28 rounded-lg align-middle opacity-60"></span>`;
    });
    ["fin-cobrado-sub", "fin-porcobrar-sub", "fin-fallido-sub", "fin-envios-sub",
      "fin-com-pagar-sub", "fin-com-pend-sub", "fin-com-total-sub", "fin-mov-count"].forEach((id) => ($(id).textContent = ""));
    $("fin-gastos").innerHTML = `<div class="skeleton my-3 h-5 rounded-md"></div>`.repeat(4);
    $("fin-env-resumen").innerHTML = "";
    $("fin-env-count").textContent = "";
    $("fin-env-lista").innerHTML = `<div class="px-5 py-3 sm:px-7"><div class="skeleton h-10 rounded-md"></div></div>`.repeat(4);
    $("fin-movimientos").innerHTML = `<tr><td colspan="3" class="px-5 py-3 sm:px-7"><div class="skeleton h-5 rounded-md"></div></td></tr>`.repeat(4);
    $("fin-avisos").classList.add("hidden");
  }

  function renderError(msg) {
    LOADING_IDS.forEach((id) => ($(id).textContent = "–"));
    $("fin-gastos").innerHTML = "";
    $("fin-movimientos").innerHTML = "";
    $("fin-env-lista").innerHTML = "";
    showNotices([msg], "error");
  }

  function showNotices(list, kind = "warn") {
    const box = $("fin-avisos");
    if (!list.length) {
      box.classList.add("hidden");
      return;
    }
    const cls = kind === "error" ? "border-red-200 bg-red-50 text-red-700" : "border-amber-200 bg-amber-50 text-amber-800";
    box.innerHTML = list.map((m) => `<div class="rounded-xl border ${cls} px-4 py-3 text-sm">${escapeHtml(m)}</div>`).join("");
    box.classList.remove("hidden");
  }

  function render(f) {
    // Balance
    if (f.balance === null || f.balance === undefined) {
      $("fin-saldo").textContent = "–";
      $("fin-saldo-sub").textContent = f.balance_error ? `No se pudo consultar: ${f.balance_error}` : "Sin dato";
    } else {
      $("fin-saldo").textContent = money(f.balance);
      $("fin-saldo-sub").textContent = "Disponible para generar guías";
    }

    // Commission
    const c = f.commission || {};
    $("fin-com-unit").textContent = money(c.per_unit);
    $("fin-com-pagar").textContent = money(c.paid_amount);
    $("fin-com-pagar-sub").textContent = plural(c.paid_units || 0, "datáfono cobrado", "datáfonos cobrados");
    $("fin-com-pend").textContent = money(c.pending_amount);
    $("fin-com-pend-sub").textContent = plural(c.pending_units || 0, "datáfono por cobrar", "datáfonos por cobrar");
    $("fin-com-total").textContent = money((c.paid_amount || 0) + (c.pending_amount || 0));
    $("fin-com-total-sub").textContent = plural((c.paid_units || 0) + (c.pending_units || 0), "datáfono", "datáfonos");
    const note = $("fin-com-nota");
    if (c.estimated > 0) {
      note.textContent = `${plural(c.estimated, "envío", "envíos")} sin cantidad confirmada se contaron como 1 datáfono. Toca Actualizar en un momento para completar el dato.`;
      note.classList.remove("hidden");
    } else {
      note.classList.add("hidden");
    }

    // Cash on delivery
    const kpi = (id, m, subText) => {
      $(id).textContent = money(m.amount);
      $(`${id}-sub`).textContent = plural(m.count || 0, subText, `${subText}s`);
    };
    kpi("fin-cobrado", f.collected || {}, "envío");
    kpi("fin-porcobrar", f.pending || {}, "envío");
    kpi("fin-fallido", f.failed || {}, "envío");
    $("fin-envios").textContent = f.shipments || 0;
    $("fin-envios-sub").textContent = f.cancelled ? `${plural(f.cancelled, "cancelado", "cancelados")}` : "Ninguno cancelado";

    renderPerShipment(f);
    renderExpenses(f);
    renderMovements(f.movements || []);
    renderExtras(f.extra_charges || []);

    const notices = [...(f.warnings || [])];
    if (f.truncated) notices.push("Hay más de 300 envíos: los más antiguos no entran en el cálculo.");
    showNotices(notices);
  }

  // ---------- Per-shipment account ----------

  const COD_STATUS_LABELS = {
    collected: { label: "Cobrado", cls: "bg-emerald-50 text-emerald-700" },
    pending: { label: "Por cobrar", cls: "bg-slate-100 text-slate-600" },
    failed: { label: "No se cobró", cls: "bg-rose-50 text-rose-700" },
    cancelled: { label: "Cancelada", cls: "bg-slate-100 text-slate-500" },
  };

  function fmtShortDay(iso) {
    const d = new Date(iso);
    if (isNaN(d)) return iso || "";
    return d.toLocaleDateString("es-CO", { timeZone: TZ, day: "numeric", month: "short" });
  }

  // What happens to a shipment's money. What is collected from the customer (product + shipping) goes to
  // Skydropx's available balance, which the API doesn't expose: it's computed from what the customer pays.
  // What actually came out of the credits for the label (label + service fee + protection);
  // if Skydropx doesn't return the movements, the shipment cost is used.
  const realCost = (e) => (e.charged > 0 ? e.charged : e.label_cost || 0) + (e.extra || 0);

  const CHARGE_CONCEPTS = { guia: "guía", servicio: "tarifa de servicio", proteccion: "Protección Plus", otro: "otros" };

  function shipmentAccount(e) {
    const cost = realCost(e);
    const credit = e.returned || 0; // refunds to the credits (e.g. cancelled label)
    switch (e.status) {
      case "collected":
        return { incoming: e.customer_pays, incomingSub: "ya está en tu saldo disponible", incomingCls: "text-emerald-600",
          net: e.customer_pays - cost + credit, netSub: "tu producto, ya sin el envío" };
      case "pending":
        return { incoming: e.customer_pays, incomingSub: "te entra cuando el cliente pague", incomingCls: "text-slate-400",
          net: e.customer_pays - cost + credit, netSub: "cuando se cobre" , isPending: true };
      case "failed":
        return { incoming: e.customer_pays, uncollected: true, incomingSub: "el cliente no pagó", incomingCls: "text-rose-600",
          net: credit - cost, netSub: "perdiste el envío" };
      case "cancelled":
        return { incoming: 0, incomingSub: "guía cancelada", incomingCls: "text-slate-400",
          net: credit - cost, netSub: credit ? "te devolvieron la guía" : "sin devolución registrada" };
      default:
        return { incoming: 0, incomingSub: "", incomingCls: "text-slate-400", net: credit - cost, netSub: "" };
    }
  }

  // What the customer pays is product + shipping. The product comes from the order; if unknown,
  // shipping is subtracted (label + service fee, without protection, which isn't charged to the customer).
  function breakdown(e) {
    const pays = e.customer_pays || 0;
    if (e.product > 0) {
      const product = Math.min(e.product, pays);
      return { product, shipping: pays - product };
    }
    const parts = e.charged_parts || {};
    const shipping = Math.min(pays, (parts.guia || e.label_cost || 0) + (parts.servicio || 0));
    return { product: pays - shipping, shipping };
  }

  const signed = (v) => `${v < 0 ? "−" : "+"}${money(Math.abs(v))}`;

  function renderPerShipment(f) {
    const list = f.per_shipment || [];
    const activeRates = list.filter((e) => e.status !== "cancelled");
    const spent = activeRates.reduce((t, e) => t + realCost(e) - (e.returned || 0), 0);
    const sum = (st) => activeRates.filter((e) => e.status === st).reduce((t, e) => t + (e.customer_pays || 0), 0);
    const collected = activeRates.filter((e) => e.status === "collected");
    const netCollected = collected.reduce((t, e) => t + shipmentAccount(e).net, 0);
    const collectedProduct = collected.reduce((t, e) => t + breakdown(e).product, 0);
    const refundedShipping = collected.reduce((t, e) => t + breakdown(e).shipping, 0);

    const box = (label, value, sub, cls) => `
      <div class="rounded-xl bg-slate-50 px-2 py-3">
        <p class="text-[11px] font-medium text-slate-500">${label}</p>
        <p class="mt-1 text-base font-bold tabular-nums sm:text-lg ${cls}">${value}</p>
        <p class="text-[11px] text-slate-400">${sub}</p>
      </div>`;
    $("fin-env-resumen").innerHTML =
      box("Pagaste en envíos", `−${money(spent)}`, plural(activeRates.length, "guía", "guías"), "text-slate-900") +
      box("Ya te entró", `+${money(sum("collected"))}`, `${money(collectedProduct)} producto + ${money(refundedShipping)} envíos`, "text-emerald-600") +
      box("Por entrar", `+${money(sum("pending"))}`, "cuando paguen", "text-slate-400") +
      box("Te quedó de lo cobrado", signed(netCollected), "ya sin el envío", "text-slate-900");

    $("fin-env-count").textContent = list.length ? plural(list.length, "envío", "envíos") : "";
    if (!list.length) {
      $("fin-env-lista").innerHTML = `<p class="px-5 py-10 text-center text-sm text-slate-400 sm:px-7">Sin envíos en este periodo</p>`;
      return;
    }
    $("fin-env-lista").innerHTML = list
      .map((e) => {
        const st = COD_STATUS_LABELS[e.status] || { label: e.status || "Sin estado", cls: "bg-slate-100 text-slate-500" };
        const c = shipmentAccount(e);
        return `
          <div class="grid grid-cols-3 gap-x-4 gap-y-2 px-5 py-3.5 text-sm sm:grid-cols-[1.6fr_1fr_1.2fr_1.1fr] sm:items-center sm:px-7 ${e.status === "cancelled" ? "opacity-60" : ""}">
            <div class="col-span-3 min-w-0 sm:col-span-1">
              <div class="flex items-center gap-2">
                <p class="truncate font-medium text-slate-800">${escapeHtml(e.customer || "Sin nombre")}</p>
                <span class="badge ${st.cls} shrink-0">${st.label}</span>
              </div>
              <p class="mt-0.5 text-xs text-slate-400">${escapeHtml(fmtShortDay(e.date))}${e.tracking ? ` · <span class="font-mono">${escapeHtml(e.tracking)}</span>` : ""}</p>
            </div>
            <div>
              <p class="text-[11px] text-slate-400">Pagaste el envío</p>
              <p class="font-semibold tabular-nums text-slate-900">−${money(realCost(e) - (e.extra || 0))}</p>
              ${Object.keys(e.charged_parts || {}).length > 1 ? Object.entries(e.charged_parts)
                .sort((a, b) => b[1] - a[1])
                .map(([k, v]) => `<p class="text-xs text-slate-500 tabular-nums">${money(v)} ${escapeHtml(CHARGE_CONCEPTS[k] || k)}</p>`).join("") : ""}
              ${e.returned > 0 ? `<p class="text-xs font-medium text-emerald-600 tabular-nums">+${money(e.returned)} devuelto${e.returned_at ? ` el ${escapeHtml(fmtShortDay(e.returned_at))}` : ""}</p>` : ""}
              ${e.extra > 0 ? `<p class="text-xs font-medium text-rose-600">−${money(e.extra)} cargo extra</p>` : ""}
            </div>
            <div>
              <p class="text-[11px] text-slate-400">Te entra</p>
              <p class="font-semibold tabular-nums ${c.incomingCls} ${c.uncollected ? "line-through" : ""}">${c.incoming ? `${c.uncollected ? "" : "+"}${money(c.incoming)}` : "—"}</p>
              ${c.incoming && !c.uncollected ? (() => {
                const d = breakdown(e);
                return `<p class="text-xs text-slate-500 tabular-nums">${money(d.product)} producto</p>
                  <p class="text-xs text-slate-500 tabular-nums">+ ${money(d.shipping)} envío ${e.status === "collected" ? "devuelto" : "a devolver"}</p>`;
              })() : ""}
              ${c.incomingSub ? `<p class="text-xs text-slate-400">${escapeHtml(c.incomingSub)}</p>` : ""}
            </div>
            <div>
              <p class="text-[11px] text-slate-400">Te queda</p>
              <p class="font-bold tabular-nums ${c.isPending ? "text-slate-400" : c.net < 0 ? "text-rose-600" : "text-slate-900"}">${signed(c.net)}</p>
              ${c.netSub ? `<p class="text-xs text-slate-400">${escapeHtml(c.netSub)}</p>` : ""}
            </div>
          </div>`;
      })
      .join("");
  }

  const CATEGORIES = {
    guia: { label: "Cobros de guías al saldo", dot: "bg-indigo-500", sign: "-" },
    proteccion: { label: "Protección Plus", dot: "bg-violet-500", sign: "-" },
    servicio: { label: "Tarifa de servicio", dot: "bg-sky-400", sign: "-" },
    fee_recarga: { label: "Comisión de Mercado Pago", dot: "bg-cyan-500", sign: "-" },
    otro: { label: "Otros cobros", dot: "bg-slate-400", sign: "-" },
    devolucion: { label: "Devoluciones", dot: "bg-teal-500", sign: "+" },
    recarga: { label: "Recargas y abonos", dot: "bg-emerald-500", sign: "+" },
  };

  function renderExpenses(f) {
    const s = f.spending || {};
    const row = (dot, label, sub, amount, cls = "text-slate-900") => `
      <div class="line-item">
        <div class="flex min-w-0 items-center gap-2.5">
          <span class="h-2.5 w-2.5 shrink-0 rounded-full ${dot}"></span>
          <div class="min-w-0"><p class="truncate font-medium text-slate-700">${escapeHtml(label)}</p>${sub ? `<p class="text-xs text-slate-400">${escapeHtml(sub)}</p>` : ""}</div>
        </div>
        <p class="shrink-0 font-semibold tabular-nums ${cls}">${amount}</p>
      </div>`;

    const lc = f.label_cost || {};
    let html = row("bg-sky-500", "Guías creadas en el periodo", `${plural(lc.count || 0, "guía", "guías")} · costo según Skydropx`, money(lc.amount));
    Object.entries(CATEGORIES).forEach(([key, cat]) => {
      const m = s[key];
      if (!m || !m.count) return;
      const positive = cat.sign === "+";
      html += row(cat.dot, cat.label, plural(m.count, "movimiento", "movimientos"),
        `${positive ? "+" : "−"}${money(m.amount)}`, positive ? "text-emerald-600" : "text-slate-900");
    });
    const ex = f.extra || {};
    html += row("bg-rose-500", "Cargos extra", plural(ex.count || 0, "cargo", "cargos"), ex.count ? `−${money(ex.amount)}` : money(0),
      ex.count ? "text-rose-600" : "text-slate-900");
    $("fin-gastos").innerHTML = html;
  }

  const KIND = {
    deposit: { label: "Abono", cls: "bg-emerald-50 text-emerald-700", sign: "+" },
    refund: { label: "Devolución", cls: "bg-teal-50 text-teal-700", sign: "+" },
    withdrawal: { label: "Cobro", cls: "bg-slate-100 text-slate-600", sign: "−" },
  };

  function renderMovements(list) {
    $("fin-mov-count").textContent = list.length ? plural(list.length, "movimiento", "movimientos") : "";
    if (!list.length) {
      $("fin-movimientos").innerHTML = `<tr><td colspan="3" class="px-5 py-10 text-center text-sm text-slate-400 sm:px-7">Sin movimientos en este periodo</td></tr>`;
      return;
    }
    $("fin-movimientos").innerHTML = list
      .map((m) => {
        const k = KIND[m.kind] || { label: m.kind || "—", cls: "bg-slate-100 text-slate-600", sign: "" };
        const cat = CATEGORIES[m.category];
        const concept = [cat && m.category !== "otro" ? cat.label : "", m.comment].filter(Boolean)[0] || "Movimiento";
        return `
          <tr class="transition hover:bg-slate-50/70">
            <td class="hidden whitespace-nowrap px-5 py-3 text-xs text-slate-500 sm:table-cell sm:px-7">${escapeHtml(fmtDateTime(m.date))}</td>
            <td class="px-5 py-3 sm:px-3">
              <p class="mb-1 text-xs text-slate-400 sm:hidden">${escapeHtml(fmtDateTime(m.date))}</p>
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="badge ${k.cls}">${escapeHtml(k.label)}</span>
                <span class="text-slate-700">${escapeHtml(concept)}</span>
              </div>
              ${m.comment && m.comment !== concept ? `<p class="mt-0.5 text-xs text-slate-500">${escapeHtml(m.comment)}</p>` : ""}
              ${m.tracking ? `<p class="mt-0.5 font-mono text-xs text-slate-400">Guía ${escapeHtml(m.tracking)}</p>` : m.kind === "refund" ? `<p class="mt-0.5 text-xs text-slate-400">Sin guía asociada${m.related ? ` · ${escapeHtml(m.related)}` : ""}</p>` : ""}
            </td>
            <td class="whitespace-nowrap px-5 py-3 text-right font-semibold tabular-nums sm:px-7 ${k.sign === "+" ? "text-emerald-600" : "text-slate-900"}">${k.sign}${money(m.amount)}</td>
          </tr>`;
      })
      .join("");
  }

  // Skydropx extra charge types in Spanish; unknown ones are shown as is.
  const CHARGE_TYPES = {
    Overweight: "Sobrepeso",
    Oversize: "Sobredimensión",
    Uncollected: "No recaudado",
    ReturnToSender: "Devolución al remitente",
    AddressCorrection: "Corrección de dirección",
  };

  function renderExtras(list) {
    $("fin-extras-box").classList.toggle("hidden", list.length === 0);
    $("fin-extras").innerHTML = list
      .map((e) => `
        <tr>
          <td class="whitespace-nowrap px-5 py-3 text-xs text-slate-500 sm:px-7">${escapeHtml(fmtDateTime(e.date))}</td>
          <td class="px-3 py-3 font-mono text-xs text-slate-600">${escapeHtml(e.tracking || "—")}</td>
          <td class="px-3 py-3 text-slate-700">${escapeHtml(CHARGE_TYPES[e.type] || e.type || "—")}</td>
          <td class="whitespace-nowrap px-5 py-3 text-right font-semibold tabular-nums text-rose-600 sm:px-7">−${money(e.amount)}</td>
        </tr>`)
      .join("");
  }

  // ---------- Events ----------

  document.querySelectorAll("[data-fin-preset]").forEach((b) => {
    b.addEventListener("click", () => setPreset(b.dataset.finPreset));
  });

  $("fin-mes").addEventListener("change", (e) => {
    if (!e.target.value) return;
    state.preset = "";
    Object.assign(state, monthBounds(e.target.value));
    syncControls();
    load();
  });

  $("fin-aplicar").addEventListener("click", () => {
    const fromDate = $("fin-desde").value;
    const toDate = $("fin-hasta").value || fromDate || today();
    if (!fromDate) {
      window.toast("Elige la fecha desde (para un solo día, pon la misma en desde y hasta).", "error");
      return;
    }
    if (fromDate > toDate) {
      window.toast("La fecha desde no puede ser posterior a la fecha hasta.", "error");
      return;
    }
    Object.assign(state, { from: fromDate, to: toDate, preset: "" });
    syncControls();
    load();
  });

  // Picking only "desde" shows that day; "hasta" can be filled in for a range.
  $("fin-desde").addEventListener("change", (e) => {
    if (e.target.value && !$("fin-hasta").value) $("fin-hasta").value = e.target.value;
  });

  $("fin-refresh").addEventListener("click", load);

  // ---------- Seller commission ----------

  async function openCommission() {
    const form = $("fin-com-form");
    form.classList.remove("hidden");
    $("fin-com-desde").value = today();
    try {
      const res = await fetch("/api/ajustes");
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudo leer la comisión");
      $("fin-com-valor").value = Math.round(body.comision || 0);
      const h = body.historial || [];
      $("fin-com-historial").textContent = h.length
        ? "Cambios: " + h.map((r) => `${money(r.value)} desde el ${fmtDay(r.from)}`).join(" · ")
        : "";
    } catch (err) {
      window.toast(err.message, "error");
    }
    $("fin-com-valor").focus();
  }

  $("fin-com-editar").addEventListener("click", openCommission);
  $("fin-com-cancelar").addEventListener("click", () => $("fin-com-form").classList.add("hidden"));
  $("fin-com-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const price = Number($("fin-com-valor").value);
    const fromDate = $("fin-com-desde").value;
    try {
      const res = await fetch("/api/ajustes/comision", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ valor: price, desde: fromDate }),
      });
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudo guardar la comisión");
      window.toast(`Comisión: ${money(price)} por datáfono desde el ${fmtDay(fromDate)}`, "success");
      $("fin-com-form").classList.add("hidden");
      load();
    } catch (err) {
      window.toast(err.message, "error");
    }
  });

  $("fin-desde").max = today();
  $("fin-hasta").max = today();
  $("fin-mes").max = today().slice(0, 7);

  window.finance = {
    show() {
      if (!state.loaded && !state.loading) setPreset(state.preset || "mes");
    },
  };
})();
