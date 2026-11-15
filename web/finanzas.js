(() => {
  const $ = (id) => document.getElementById(id);
  const TZ = "America/Bogota";

  const state = { desde: "", hasta: "", preset: "mes", loaded: false, loading: false };

  const cop = new Intl.NumberFormat("es-CO", { style: "currency", currency: "COP", maximumFractionDigits: 0 });
  const money = (v) => cop.format(Math.round(Number(v) || 0)).replace(/\s/g, " ");
  const plural = (n, uno, varios) => `${n} ${n === 1 ? uno : varios}`;

  function escapeHtml(text) {
    return String(text ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  // ---------- Fechas (hora de Colombia) ----------

  function today() {
    return new Date().toLocaleDateString("en-CA", { timeZone: TZ });
  }

  function monthBounds(ym) {
    const [y, m] = ym.split("-").map(Number);
    const last = new Date(Date.UTC(y, m, 0)).getUTCDate();
    const desde = `${ym}-01`;
    let hasta = `${ym}-${String(last).padStart(2, "0")}`;
    if (hasta > today()) hasta = today();
    return { desde, hasta };
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
    if (state.preset === "todo") return `Acumulado hasta el ${fmtDay(state.hasta)}`;
    if (state.desde === state.hasta) return state.desde === today() ? "Hoy" : fmtDay(state.desde);
    return `Del ${fmtDay(state.desde)} al ${fmtDay(state.hasta)}`;
  }

  function setPreset(preset) {
    const t = today();
    const ym = t.slice(0, 7);
    state.preset = preset;
    if (preset === "hoy") Object.assign(state, { desde: t, hasta: t });
    if (preset === "mes") Object.assign(state, monthBounds(ym));
    if (preset === "mes-anterior") Object.assign(state, monthBounds(prevMonth(ym)));
    if (preset === "todo") Object.assign(state, { desde: "", hasta: t });
    syncControls();
    load();
  }

  function syncControls() {
    document.querySelectorAll("[data-fin-preset]").forEach((b) => {
      b.setAttribute("aria-pressed", String(b.dataset.finPreset === state.preset));
    });
    $("fin-desde").value = state.desde;
    $("fin-hasta").value = state.hasta;
    const sameMonth = state.desde && state.desde.slice(0, 7) === state.hasta.slice(0, 7) && state.desde.endsWith("-01");
    $("fin-mes").value = sameMonth ? state.desde.slice(0, 7) : "";
    $("fin-periodo").textContent = periodLabel();
  }

  // ---------- Carga ----------

  async function load() {
    state.loading = true;
    renderLoading();
    const q = new URLSearchParams();
    if (state.desde) q.set("desde", state.desde);
    if (state.hasta) q.set("hasta", state.hasta);
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
    showAvisos([msg], "error");
  }

  function showAvisos(list, kind = "warn") {
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
    // Saldo
    if (f.balance === null || f.balance === undefined) {
      $("fin-saldo").textContent = "–";
      $("fin-saldo-sub").textContent = f.balance_error ? `No se pudo consultar: ${f.balance_error}` : "Sin dato";
    } else {
      $("fin-saldo").textContent = money(f.balance);
      $("fin-saldo-sub").textContent = "Disponible para generar guías";
    }

    // Comisión
    const c = f.commission || {};
    $("fin-com-unit").textContent = money(c.per_unit);
    $("fin-com-pagar").textContent = money(c.paid_amount);
    $("fin-com-pagar-sub").textContent = plural(c.paid_units || 0, "datáfono cobrado", "datáfonos cobrados");
    $("fin-com-pend").textContent = money(c.pending_amount);
    $("fin-com-pend-sub").textContent = plural(c.pending_units || 0, "datáfono por cobrar", "datáfonos por cobrar");
    $("fin-com-total").textContent = money((c.paid_amount || 0) + (c.pending_amount || 0));
    $("fin-com-total-sub").textContent = plural((c.paid_units || 0) + (c.pending_units || 0), "datáfono", "datáfonos");
    const nota = $("fin-com-nota");
    if (c.estimated > 0) {
      nota.textContent = `${plural(c.estimated, "envío", "envíos")} sin cantidad confirmada se contaron como 1 datáfono. Toca Actualizar en un momento para completar el dato.`;
      nota.classList.remove("hidden");
    } else {
      nota.classList.add("hidden");
    }

    // Contra entrega
    const kpi = (id, m, texto) => {
      $(id).textContent = money(m.amount);
      $(`${id}-sub`).textContent = plural(m.count || 0, texto, `${texto}s`);
    };
    kpi("fin-cobrado", f.collected || {}, "envío");
    kpi("fin-porcobrar", f.pending || {}, "envío");
    kpi("fin-fallido", f.failed || {}, "envío");
    $("fin-envios").textContent = f.shipments || 0;
    $("fin-envios-sub").textContent = f.cancelled ? `${plural(f.cancelled, "cancelado", "cancelados")}` : "Ninguno cancelado";

    renderPorEnvio(f);
    renderGastos(f);
    renderMovimientos(f.movements || []);
    renderExtras(f.extra_charges || []);

    const avisos = [...(f.warnings || [])];
    if (f.truncated) avisos.push("Hay más de 300 envíos: los más antiguos no entran en el cálculo.");
    showAvisos(avisos);
  }

  // ---------- Cuenta por envío ----------

  const ESTADO_COBRO = {
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

  // Lo que pasa con la plata de un envío. Lo cobrado al cliente (producto + envío) llega al saldo
  // disponible de Skydropx, que la API no deja consultar: se calcula con lo que paga el cliente.
  // Lo que realmente salió de los créditos por la guía (guía + tarifa de servicio + protección);
  // si Skydropx no trae los movimientos, se usa el costo del envío.
  const costoReal = (e) => (e.charged > 0 ? e.charged : e.label_cost || 0) + (e.extra || 0);

  const CONCEPTO_COBRO = { guia: "guía", servicio: "tarifa de servicio", proteccion: "Protección Plus", otro: "otros" };

  function cuentaEnvio(e) {
    const costo = costoReal(e);
    const credito = e.returned || 0; // devoluciones a los créditos (p. ej. guía cancelada)
    switch (e.status) {
      case "collected":
        return { entra: e.customer_pays, entraSub: "ya está en tu saldo disponible", entraCls: "text-emerald-600",
          queda: e.customer_pays - costo + credito, quedaSub: "tu producto, ya sin el envío" };
      case "pending":
        return { entra: e.customer_pays, entraSub: "te entra cuando el cliente pague", entraCls: "text-slate-400",
          queda: e.customer_pays - costo + credito, quedaSub: "cuando se cobre" , pendiente: true };
      case "failed":
        return { entra: e.customer_pays, noCobrado: true, entraSub: "el cliente no pagó", entraCls: "text-rose-600",
          queda: credito - costo, quedaSub: "perdiste el envío" };
      case "cancelled":
        return { entra: 0, entraSub: "guía cancelada", entraCls: "text-slate-400",
          queda: credito - costo, quedaSub: credito ? "te devolvieron la guía" : "sin devolución registrada" };
      default:
        return { entra: 0, entraSub: "", entraCls: "text-slate-400", queda: credito - costo, quedaSub: "" };
    }
  }

  // Lo que paga el cliente es producto + envío. El producto sale de la orden; si no se conoce,
  // se descuenta el envío (guía + tarifa de servicio, sin la protección, que no se le cobra al cliente).
  function desglose(e) {
    const paga = e.customer_pays || 0;
    if (e.product > 0) {
      const producto = Math.min(e.product, paga);
      return { producto, envio: paga - producto };
    }
    const parts = e.charged_parts || {};
    const envio = Math.min(paga, (parts.guia || e.label_cost || 0) + (parts.servicio || 0));
    return { producto: paga - envio, envio };
  }

  const signed = (v) => `${v < 0 ? "−" : "+"}${money(Math.abs(v))}`;

  function renderPorEnvio(f) {
    const list = f.per_shipment || [];
    const activos = list.filter((e) => e.status !== "cancelled");
    const gastado = activos.reduce((t, e) => t + costoReal(e) - (e.returned || 0), 0);
    const sum = (st) => activos.filter((e) => e.status === st).reduce((t, e) => t + (e.customer_pays || 0), 0);
    const cobrados = activos.filter((e) => e.status === "collected");
    const netoCobrados = cobrados.reduce((t, e) => t + cuentaEnvio(e).queda, 0);
    const prodCobrado = cobrados.reduce((t, e) => t + desglose(e).producto, 0);
    const envioDevuelto = cobrados.reduce((t, e) => t + desglose(e).envio, 0);

    const box = (label, value, sub, cls) => `
      <div class="rounded-xl bg-slate-50 px-2 py-3">
        <p class="text-[11px] font-medium text-slate-500">${label}</p>
        <p class="mt-1 text-base font-bold tabular-nums sm:text-lg ${cls}">${value}</p>
        <p class="text-[11px] text-slate-400">${sub}</p>
      </div>`;
    $("fin-env-resumen").innerHTML =
      box("Pagaste en envíos", `−${money(gastado)}`, plural(activos.length, "guía", "guías"), "text-slate-900") +
      box("Ya te entró", `+${money(sum("collected"))}`, `${money(prodCobrado)} producto + ${money(envioDevuelto)} envíos`, "text-emerald-600") +
      box("Por entrar", `+${money(sum("pending"))}`, "cuando paguen", "text-slate-400") +
      box("Te quedó de lo cobrado", signed(netoCobrados), "ya sin el envío", "text-slate-900");

    $("fin-env-count").textContent = list.length ? plural(list.length, "envío", "envíos") : "";
    if (!list.length) {
      $("fin-env-lista").innerHTML = `<p class="px-5 py-10 text-center text-sm text-slate-400 sm:px-7">Sin envíos en este periodo</p>`;
      return;
    }
    $("fin-env-lista").innerHTML = list
      .map((e) => {
        const st = ESTADO_COBRO[e.status] || { label: e.status || "Sin estado", cls: "bg-slate-100 text-slate-500" };
        const c = cuentaEnvio(e);
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
              <p class="font-semibold tabular-nums text-slate-900">−${money(costoReal(e) - (e.extra || 0))}</p>
              ${Object.keys(e.charged_parts || {}).length > 1 ? Object.entries(e.charged_parts)
                .sort((a, b) => b[1] - a[1])
                .map(([k, v]) => `<p class="text-xs text-slate-500 tabular-nums">${money(v)} ${escapeHtml(CONCEPTO_COBRO[k] || k)}</p>`).join("") : ""}
              ${e.returned > 0 ? `<p class="text-xs font-medium text-emerald-600 tabular-nums">+${money(e.returned)} devuelto${e.returned_at ? ` el ${escapeHtml(fmtShortDay(e.returned_at))}` : ""}</p>` : ""}
              ${e.extra > 0 ? `<p class="text-xs font-medium text-rose-600">−${money(e.extra)} cargo extra</p>` : ""}
            </div>
            <div>
              <p class="text-[11px] text-slate-400">Te entra</p>
              <p class="font-semibold tabular-nums ${c.entraCls} ${c.noCobrado ? "line-through" : ""}">${c.entra ? `${c.noCobrado ? "" : "+"}${money(c.entra)}` : "—"}</p>
              ${c.entra && !c.noCobrado ? (() => {
                const d = desglose(e);
                return `<p class="text-xs text-slate-500 tabular-nums">${money(d.producto)} producto</p>
                  <p class="text-xs text-slate-500 tabular-nums">+ ${money(d.envio)} envío ${e.status === "collected" ? "devuelto" : "a devolver"}</p>`;
              })() : ""}
              ${c.entraSub ? `<p class="text-xs text-slate-400">${escapeHtml(c.entraSub)}</p>` : ""}
            </div>
            <div>
              <p class="text-[11px] text-slate-400">Te queda</p>
              <p class="font-bold tabular-nums ${c.pendiente ? "text-slate-400" : c.queda < 0 ? "text-rose-600" : "text-slate-900"}">${signed(c.queda)}</p>
              ${c.quedaSub ? `<p class="text-xs text-slate-400">${escapeHtml(c.quedaSub)}</p>` : ""}
            </div>
          </div>`;
      })
      .join("");
  }

  const CATEGORIAS = {
    guia: { label: "Cobros de guías al saldo", dot: "bg-indigo-500", sign: "-" },
    proteccion: { label: "Protección Plus", dot: "bg-violet-500", sign: "-" },
    servicio: { label: "Tarifa de servicio", dot: "bg-sky-400", sign: "-" },
    fee_recarga: { label: "Comisión de Mercado Pago", dot: "bg-cyan-500", sign: "-" },
    otro: { label: "Otros cobros", dot: "bg-slate-400", sign: "-" },
    devolucion: { label: "Devoluciones", dot: "bg-teal-500", sign: "+" },
    recarga: { label: "Recargas y abonos", dot: "bg-emerald-500", sign: "+" },
  };

  function renderGastos(f) {
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
    Object.entries(CATEGORIAS).forEach(([key, cat]) => {
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

  function renderMovimientos(list) {
    $("fin-mov-count").textContent = list.length ? plural(list.length, "movimiento", "movimientos") : "";
    if (!list.length) {
      $("fin-movimientos").innerHTML = `<tr><td colspan="3" class="px-5 py-10 text-center text-sm text-slate-400 sm:px-7">Sin movimientos en este periodo</td></tr>`;
      return;
    }
    $("fin-movimientos").innerHTML = list
      .map((m) => {
        const k = KIND[m.kind] || { label: m.kind || "—", cls: "bg-slate-100 text-slate-600", sign: "" };
        const cat = CATEGORIAS[m.category];
        const concepto = [cat && m.category !== "otro" ? cat.label : "", m.comment].filter(Boolean)[0] || "Movimiento";
        return `
          <tr class="transition hover:bg-slate-50/70">
            <td class="hidden whitespace-nowrap px-5 py-3 text-xs text-slate-500 sm:table-cell sm:px-7">${escapeHtml(fmtDateTime(m.date))}</td>
            <td class="px-5 py-3 sm:px-3">
              <p class="mb-1 text-xs text-slate-400 sm:hidden">${escapeHtml(fmtDateTime(m.date))}</p>
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="badge ${k.cls}">${escapeHtml(k.label)}</span>
                <span class="text-slate-700">${escapeHtml(concepto)}</span>
              </div>
              ${m.comment && m.comment !== concepto ? `<p class="mt-0.5 text-xs text-slate-500">${escapeHtml(m.comment)}</p>` : ""}
              ${m.tracking ? `<p class="mt-0.5 font-mono text-xs text-slate-400">Guía ${escapeHtml(m.tracking)}</p>` : m.kind === "refund" ? `<p class="mt-0.5 text-xs text-slate-400">Sin guía asociada${m.related ? ` · ${escapeHtml(m.related)}` : ""}</p>` : ""}
            </td>
            <td class="whitespace-nowrap px-5 py-3 text-right font-semibold tabular-nums sm:px-7 ${k.sign === "+" ? "text-emerald-600" : "text-slate-900"}">${k.sign}${money(m.amount)}</td>
          </tr>`;
      })
      .join("");
  }

  // Tipos de cargo extra de Skydropx en español; los desconocidos se muestran tal cual.
  const TIPO_CARGO = {
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
          <td class="px-3 py-3 text-slate-700">${escapeHtml(TIPO_CARGO[e.type] || e.type || "—")}</td>
          <td class="whitespace-nowrap px-5 py-3 text-right font-semibold tabular-nums text-rose-600 sm:px-7">−${money(e.amount)}</td>
        </tr>`)
      .join("");
  }

  // ---------- Eventos ----------

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
    const desde = $("fin-desde").value;
    const hasta = $("fin-hasta").value || desde || today();
    if (!desde) {
      window.toast("Elige la fecha desde (para un solo día, pon la misma en desde y hasta).", "error");
      return;
    }
    if (desde > hasta) {
      window.toast("La fecha desde no puede ser posterior a la fecha hasta.", "error");
      return;
    }
    Object.assign(state, { desde, hasta, preset: "" });
    syncControls();
    load();
  });

  // Elegir solo "desde" muestra ese día; se puede completar "hasta" para un rango.
  $("fin-desde").addEventListener("change", (e) => {
    if (e.target.value && !$("fin-hasta").value) $("fin-hasta").value = e.target.value;
  });

  $("fin-refresh").addEventListener("click", load);

  // ---------- Comisión del vendedor ----------

  async function abrirComision() {
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

  $("fin-com-editar").addEventListener("click", abrirComision);
  $("fin-com-cancelar").addEventListener("click", () => $("fin-com-form").classList.add("hidden"));
  $("fin-com-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const valor = Number($("fin-com-valor").value);
    const desde = $("fin-com-desde").value;
    try {
      const res = await fetch("/api/ajustes/comision", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ valor, desde }),
      });
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudo guardar la comisión");
      window.toast(`Comisión: ${money(valor)} por datáfono desde el ${fmtDay(desde)}`, "success");
      $("fin-com-form").classList.add("hidden");
      load();
    } catch (err) {
      window.toast(err.message, "error");
    }
  });

  $("fin-desde").max = today();
  $("fin-hasta").max = today();
  $("fin-mes").max = today().slice(0, 7);

  window.finanzas = {
    show() {
      if (!state.loaded && !state.loading) setPreset(state.preset || "mes");
    },
  };
})();
