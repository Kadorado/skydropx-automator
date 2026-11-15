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
    $("fin-movimientos").innerHTML = `<tr><td colspan="3" class="px-5 py-3 sm:px-7"><div class="skeleton h-5 rounded-md"></div></td></tr>`.repeat(4);
    $("fin-avisos").classList.add("hidden");
  }

  function renderError(msg) {
    LOADING_IDS.forEach((id) => ($(id).textContent = "–"));
    $("fin-gastos").innerHTML = "";
    $("fin-movimientos").innerHTML = "";
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

    renderGastos(f);
    renderMovimientos(f.movements || []);
    renderExtras(f.extra_charges || []);

    const avisos = [...(f.warnings || [])];
    if (f.truncated) avisos.push("Hay más de 300 envíos: los más antiguos no entran en el cálculo.");
    showAvisos(avisos);
  }

  const CATEGORIAS = {
    guia: { label: "Cobros de guías al saldo", dot: "bg-indigo-500", sign: "-" },
    proteccion: { label: "Protección Plus", dot: "bg-violet-500", sign: "-" },
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
              ${m.tracking ? `<p class="mt-0.5 font-mono text-xs text-slate-400">${escapeHtml(m.tracking)}</p>` : ""}
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

  $("fin-desde").max = today();
  $("fin-hasta").max = today();
  $("fin-mes").max = today().slice(0, 7);

  window.finanzas = {
    show() {
      if (!state.loaded && !state.loading) setPreset(state.preset || "mes");
    },
  };
})();
