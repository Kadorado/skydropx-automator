(() => {
  const $ = (id) => document.getElementById(id);

  const SELLER_PHONE_KEY = "mis-envios-vendedor-telefono";
  const DEFAULT_SELLER_PHONE = "3145319944"; // +57 314 531 9944 (el prefijo 57 lo agrega el enlace)
  const TELEGRAM_CHAT_URL = "https://web.telegram.org/a/#5213739536"; // chat del vendedor en Telegram Web
  const PAGE_SIZE = 10;
  const TZ = "America/Bogota";

  // Página de seguimiento por transportadora (si Skydropx no la envía en el paquete).
  const CARRIER_TRACKING = {
    interrapidisimo: "https://siguetuenvio.interrapidisimo.com/",
    servientrega: "https://www.servientrega.com/wps/portal",
    envia: "https://envia.co/",
    coordinadora: "https://coordinadora.com/",
  };

  // Formato de WhatsApp: *negrita*, saltos de línea y emojis.
  const TEMPLATE =
    "✅ *Envío creado con éxito*\n\n" +
    "📦 *Guía:* {guia}\n" +
    "👤 *Cliente:* {nombre}\n" +
    "📍 *Destino:* {destino}\n" +
    "🚚 *Transportadora:* {transportadora}\n" +
    "📊 *Estado:* {estado}\n" +
    "💰 *Cobro:* {cobro}\n" +
    "🔎 *Seguimiento:* {seguimiento}\n\n" +
    "📄 *Descarga la guía (PDF):*\n" +
    "{pdf}";

  const STATUS = {
    success: { label: "Listo", cls: "bg-emerald-100 text-emerald-700" },
    completed: { label: "Completado", cls: "bg-emerald-100 text-emerald-700" },
    created: { label: "Creado", cls: "bg-slate-100 text-slate-700" },
    pending: { label: "Pendiente", cls: "bg-amber-100 text-amber-800" },
    cancelled: { label: "Cancelado", cls: "bg-red-100 text-red-700" },
  };

  // Nombres legibles de las transportadoras que devuelve Skydropx.
  const CARRIER_NAMES = {
    interrapidisimo: "Interrapidísimo",
    servientrega: "Servientrega",
    coordinadora: "Coordinadora",
    envia: "Envía",
    fedex: "FedEx",
    ups: "UPS",
  };

  function carrierName(name) {
    const key = carrierKey(name);
    if (CARRIER_NAMES[key]) return CARRIER_NAMES[key];
    return String(name || "").replace(/\b\w/g, (c) => c.toUpperCase());
  }

  const state = {
    qty: {}, // cantidad de artículos por ID de orden
    qtyRequested: new Set(),
    items: [],
    included: [],
    loaded: false,
    filter: "today", // today | yesterday | all | date
    day: "",
    search: "",
    page: 1,
    selectedId: null,
    status: {},
    statusLoaded: false,
    estado: "",
    cobro: "",
  };

  // ---------- Utilidades ----------

  function todayBogota() {
    return new Date().toLocaleDateString("en-CA", { timeZone: TZ });
  }

  function yesterdayBogota() {
    return new Date(Date.now() - 86400000).toLocaleDateString("en-CA", { timeZone: TZ });
  }

  function dayOf(iso) {
    if (!iso) return "";
    const d = new Date(iso);
    if (isNaN(d)) return iso.slice(0, 10);
    return d.toLocaleDateString("en-CA", { timeZone: TZ });
  }

  function timeOf(iso) {
    const d = new Date(iso);
    if (isNaN(d)) return "";
    return d.toLocaleTimeString("es-CO", { timeZone: TZ, hour: "2-digit", minute: "2-digit" });
  }

  function dayLabel(iso) {
    const day = dayOf(iso);
    if (day === todayBogota()) return "Hoy";
    if (day === yesterdayBogota()) return "Ayer";
    return day;
  }

  function escapeHtml(text) {
    return String(text ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  function carrierKey(name) {
    return String(name || "")
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .toLowerCase()
      .replace(/[^a-z]/g, "");
  }

  function recipientOf(shipment) {
    const rel = shipment.relationships && shipment.relationships.address_to && shipment.relationships.address_to.data;
    if (!rel) return {};
    const addr = state.included.find((i) => i.type === "address" && i.id === rel.id);
    return (addr && addr.attributes) || {};
  }

  function packageOf(shipment) {
    const rel = shipment.relationships && shipment.relationships.packages && shipment.relationships.packages.data;
    const ref = rel && rel[0];
    if (!ref) return {};
    const pkg = state.included.find((i) => i.type === "package" && i.id === ref.id);
    return (pkg && pkg.attributes) || {};
  }

  function destinoOf(to) {
    return [to.area_level2, to.area_level1].filter(Boolean).join(", ");
  }

  const ESTADO = {
    en_camino: { label: "En camino", cls: "bg-sky-100 text-sky-700" },
    entregado: { label: "Entregado", cls: "bg-emerald-100 text-emerald-700" },
    intento_entrega: { label: "Intento de entrega", cls: "bg-orange-100 text-orange-700" },
    en_proceso: { label: "En proceso", cls: "bg-slate-100 text-slate-700" },
    creado: { label: "Creado", cls: "bg-slate-100 text-slate-700" },
    cancelado: { label: "Cancelado", cls: "bg-red-100 text-red-700" },
    sin_guia: { label: "Sin guía", cls: "bg-slate-100 text-slate-500" },
    sin_estado: { label: "Sin rastreo", cls: "bg-slate-100 text-slate-500" },
  };

  const COBRO = {
    cobrado: { label: "Cobrado", cls: "bg-teal-100 text-teal-700" },
    por_cobrar: { label: "Por cobrar", cls: "bg-amber-100 text-amber-800" },
  };

  function statusOf(s) {
    return state.status[s.id];
  }

  // Cobro para el mensaje: usa el rastreo si ya cargó; si no, el estado contra entrega del propio envío.
  function cobroTexto(s) {
    const st = statusOf(s);
    if (st?.cobro === "no_aplica" && st.key === "cancelado") return "No aplica (envío cancelado)";
    const key = COBRO[st?.cobro] ? st.cobro : { collected: "cobrado", pending: "por_cobrar" }[s.attributes?.on_delivery_status];
    if (key === "cobrado") return "Cobrado (pago contra entrega)";
    if (key === "por_cobrar") return "Por cobrar (pago contra entrega)";
    return "No aplica";
  }

  // Estado logístico + cobro. Muestra un placeholder mientras llega el rastreo.
  function estadoCell(s) {
    const st = statusOf(s);
    if (!st) {
      return state.statusLoaded
        ? `<span class="text-slate-400">—</span>`
        : `<span class="skeleton inline-block h-5 w-24 rounded-full"></span>`;
    }
    const e = ESTADO[st.key] || { label: st.label || "—", cls: "bg-slate-100 text-slate-700" };
    const c = COBRO[st.cobro];
    return `<div class="flex flex-col items-start gap-1.5"><span class="badge ${e.cls}">${escapeHtml(e.label)}</span>${c ? `<span class="badge ${c.cls}">${escapeHtml(c.label)}</span>` : ""}</div>`;
  }

  function fillTemplate(template, data) {
    let msg = template;
    Object.keys(data).forEach((k) => {
      msg = msg.replaceAll(`{${k}}`, data[k] || "");
    });
    return msg;
  }

  // ---------- Filtros y datos visibles ----------

  function filtered() {
    let list = state.items;
    if (state.filter === "today") list = list.filter((s) => dayOf(s.attributes.created_at) === todayBogota());
    if (state.filter === "yesterday") list = list.filter((s) => dayOf(s.attributes.created_at) === yesterdayBogota());
    if (state.filter === "date" && state.day) list = list.filter((s) => dayOf(s.attributes.created_at) === state.day);
    if (state.estado) list = list.filter((s) => statusOf(s)?.key === state.estado);
    if (state.cobro) list = list.filter((s) => statusOf(s)?.cobro === state.cobro);

    const q = state.search.trim().toLowerCase();
    if (q) {
      list = list.filter((s) => {
        const a = s.attributes || {};
        const to = recipientOf(s);
        const haystack = [a.master_tracking_number, to.name, to.area_level2, to.area_level1, a.carrier_name]
          .filter(Boolean)
          .join(" ")
          .toLowerCase();
        return haystack.includes(q);
      });
    }
    return list;
  }

  // ---------- Carga ----------

  async function loadShipments() {
    state.loaded = false;
    state.statusLoaded = false;
    renderLoading();
    try {
      const res = await fetch("/api/skydropx/shipments");
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudieron cargar los envíos");
      state.included = body.included || [];
      state.items = (body.data || []).slice().sort(
        (a, b) => new Date(b.attributes.created_at) - new Date(a.attributes.created_at)
      );
      state.truncated = !!body.truncated;
      state.loaded = true;
      state.page = 1;
      render();
      loadStatuses();
    } catch (err) {
      renderError(err.message);
      window.toast(err.message, "error");
    }
  }

  async function loadStatuses() {
    try {
      const res = await fetch("/api/skydropx/status");
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudo consultar el rastreo");
      state.status = body.statuses || {};
      state.statusLoaded = true;
      render();
    } catch (err) {
      state.statusLoaded = true;
      render();
      window.toast(`Rastreo: ${err.message}`, "error");
    }
  }

  // ---------- Render ----------

  function render() {
    const all = filtered();
    const pages = Math.max(1, Math.ceil(all.length / PAGE_SIZE));
    if (state.page > pages) state.page = pages;
    const pageItems = all.slice((state.page - 1) * PAGE_SIZE, state.page * PAGE_SIZE);

    const scope =
      state.filter === "today" ? "de hoy" :
      state.filter === "yesterday" ? "de ayer" :
      state.filter === "date" && state.day ? `del ${state.day}` : "en total";
    $("shipments-count").textContent =
      `${all.length} envío${all.length === 1 ? "" : "s"} ${scope}` +
      (state.truncated ? " · se muestran los primeros 300" : "");

    renderTable(pageItems);
    renderCards(pageItems);
    renderPagination(all.length, pages);
    syncChips();
    loadQuantities(pageItems);
  }

  // Cantidad de artículos: Skydropx la guarda en la orden, así que se consulta solo para la página visible.
  async function loadQuantities(items) {
    const ids = items
      .map((s) => s.attributes?.order_id)
      .filter((id) => id && !state.qtyRequested.has(id));
    if (ids.length === 0) return;
    ids.forEach((id) => state.qtyRequested.add(id));
    try {
      const res = await fetch(`/api/skydropx/order-quantities?ids=${ids.map(encodeURIComponent).join(",")}`);
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || "No se pudo consultar la cantidad");
      Object.assign(state.qty, body.quantities || {});
    } catch (err) {
      console.warn("Cantidad de artículos:", err.message);
    }
    ids.forEach((id) => {
      if (!(id in state.qty)) state.qty[id] = null; // sin dato: se muestra "—"
    });
    render();
  }

  function qtyCell(s) {
    const id = s.attributes?.order_id;
    if (!id) return `<span class="text-slate-400">—</span>`;
    if (!(id in state.qty)) return `<span class="skeleton inline-block h-4 w-6 rounded"></span>`;
    const q = state.qty[id];
    return q ? `<span class="font-semibold text-slate-800">${q}</span>` : `<span class="text-slate-400">—</span>`;
  }

  function qtyText(s) {
    const q = state.qty[s.attributes?.order_id];
    return q ? `${q} artículo${q === 1 ? "" : "s"}` : "";
  }

  function renderLoading() {
    const row = `<tr><td colspan="7" class="px-5 py-4"><div class="skeleton h-5 rounded-md"></div></td></tr>`;
    $("orders-list").innerHTML = row.repeat(5);
    $("shipments-cards").innerHTML = `<div class="skeleton h-28 rounded-2xl"></div>`.repeat(3);
    $("shipments-count").textContent = "Cargando envíos…";
  }

  function renderError(msg) {
    const html = `<div class="rounded-2xl border border-red-200 bg-red-50 p-6 text-center text-sm text-red-700">${escapeHtml(msg)}</div>`;
    $("shipments-cards").innerHTML = html;
    $("orders-list").innerHTML = `<tr><td colspan="7" class="p-0">${html}</td></tr>`;
    $("shipments-count").textContent = "No se pudieron cargar los envíos";
  }

  function emptyState() {
    return `
      <div class="flex flex-col items-center gap-3 py-12 text-center">
        <div class="flex h-12 w-12 items-center justify-center rounded-full bg-slate-100 text-slate-400">
          <svg viewBox="0 0 24 24" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8 12 3 3 8v8l9 5 9-5V8Z"/><path d="m3.3 7.3 8.7 4.7 8.7-4.7M12 22V12"/></svg>
        </div>
        <p class="text-sm font-medium text-slate-700">No hay envíos para este filtro</p>
        <button type="button" data-action="all" class="btn-ghost !py-1.5 text-xs">Ver todos los envíos</button>
      </div>`;
  }

  function renderTable(items) {
    const tbody = $("orders-list");
    if (!state.loaded) return;
    if (items.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7">${emptyState()}</td></tr>`;
      return;
    }
    tbody.innerHTML = items
      .map((s) => {
        const a = s.attributes || {};
        const to = recipientOf(s);
        const guia = a.master_tracking_number
          ? `<span class="font-mono text-slate-800">${escapeHtml(a.master_tracking_number)}</span>`
          : `<span class="text-slate-400">Sin guía</span>`;
        return `
          <tr class="group transition hover:bg-indigo-50/40">
            <td class="whitespace-nowrap px-5 py-3.5">
              <div class="font-medium text-slate-800">${escapeHtml(dayLabel(a.created_at))}</div>
              <div class="text-xs text-slate-400">${escapeHtml(timeOf(a.created_at))}</div>
            </td>
            <td class="whitespace-nowrap px-5 py-3.5">${guia}</td>
            <td class="px-5 py-3.5 font-medium text-slate-800">${escapeHtml(to.name || "—")}</td>
            <td class="px-5 py-3.5 text-slate-500">${escapeHtml(destinoOf(to) || "—")}</td>
            <td class="px-5 py-3.5 text-center">${qtyCell(s)}</td>
            <td class="px-5 py-3.5">${estadoCell(s)}</td>
            <td class="whitespace-nowrap px-5 py-3.5 text-right">
              <button type="button" data-action="pdf" data-id="${s.id}" class="btn-ghost !px-3 !py-1.5 text-xs">PDF</button>
              <button type="button" data-action="msg" data-id="${s.id}" class="btn-primary ml-2 !px-3 !py-1.5 text-xs">Mensaje</button>
            </td>
          </tr>`;
      })
      .join("");
  }

  function renderCards(items) {
    const box = $("shipments-cards");
    if (!state.loaded) return;
    if (items.length === 0) {
      box.innerHTML = `<div class="card">${emptyState()}</div>`;
      return;
    }
    box.innerHTML = items
      .map((s) => {
        const a = s.attributes || {};
        const to = recipientOf(s);
        return `
          <div class="card !p-4">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="font-mono text-sm font-semibold text-slate-900">${a.master_tracking_number ? escapeHtml(a.master_tracking_number) : "Sin guía"}</p>
                <p class="mt-0.5 truncate text-sm text-slate-600">${escapeHtml(to.name || "—")}</p>
                <p class="mt-0.5 text-xs text-slate-400">${escapeHtml(destinoOf(to) || "—")} · ${escapeHtml(dayLabel(a.created_at))} ${escapeHtml(timeOf(a.created_at))}${qtyText(s) ? ` · ${escapeHtml(qtyText(s))}` : ""}</p>
              </div>
              ${estadoCell(s)}
            </div>
            <div class="mt-4 grid grid-cols-2 gap-2">
              <button type="button" data-action="pdf" data-id="${s.id}" class="btn-ghost !py-2 text-sm">PDF</button>
              <button type="button" data-action="msg" data-id="${s.id}" class="btn-primary !py-2 text-sm">Mensaje</button>
            </div>
          </div>`;
      })
      .join("");
  }

  function renderPagination(total, pages) {
    const from = total === 0 ? 0 : (state.page - 1) * PAGE_SIZE + 1;
    const to = Math.min(state.page * PAGE_SIZE, total);
    $("page-info").textContent = total === 0 ? "" : `${from}–${to} de ${total} · Página ${state.page} de ${pages}`;
    $("btn-prev-page").disabled = state.page <= 1;
    $("btn-next-page").disabled = state.page >= pages;
  }

  function syncChips() {
    const pressed = {
      today: state.filter === "today",
      yesterday: state.filter === "yesterday",
      all: state.filter === "all",
    };
    $("btn-filter-today").setAttribute("aria-pressed", String(pressed.today));
    $("btn-filter-yesterday").setAttribute("aria-pressed", String(pressed.yesterday));
    $("btn-filter-all").setAttribute("aria-pressed", String(pressed.all));
  }

  // ---------- PDF ----------

  async function downloadLabel(id) {
    try {
      const res = await fetch(`/api/skydropx/shipments/${encodeURIComponent(id)}/label`);
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || "No se pudo descargar la guía");
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `guia-${id}.pdf`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
    } catch (err) {
      window.toast(err.message, "error");
    }
  }

  // ---------- Panel de mensaje ----------

  function openDrawer(shipment) {
    state.selectedId = shipment.id;
    const a = shipment.attributes || {};
    const to = recipientOf(shipment);
    const pkg = packageOf(shipment);

    const data = {
      guia: a.master_tracking_number || "pendiente",
      nombre: to.name || "",
      destino: destinoOf(to),
      transportadora: [carrierName(a.carrier_name), a.service_name].filter(Boolean).join(" - ") || "Interrapidísimo",
      seguimiento: pkg.tracking_url_provider || CARRIER_TRACKING[carrierKey(a.carrier_name)] || "pendiente",
      estado: (statusOf(shipment) && (ESTADO[statusOf(shipment).key]?.label || statusOf(shipment).label)) || "pendiente de rastreo",
      cobro: cobroTexto(shipment),
      pdf: pkg.label_url || "pendiente (la guía aún no está disponible)",
    };

    $("od-title").textContent = `Envío ${data.guia}`;
    $("od-info").textContent = `${data.nombre || "Sin nombre"} · ${data.destino || "Sin destino"} · ${dayLabel(a.created_at)} ${timeOf(a.created_at)}`;

    const st = statusOf(shipment);
    const e = st && (ESTADO[st.key] || { label: st.label, cls: "bg-slate-100 text-slate-700" });
    const c = st && COBRO[st.cobro];
    $("od-status").innerHTML = st
      ? `<div class="flex flex-wrap items-center gap-2">${e ? `<span class="badge ${e.cls}">${escapeHtml(e.label)}</span>` : ""}${c ? `<span class="badge ${c.cls}">${escapeHtml(c.label)}</span>` : ""}</div>` +
        (st.last_event
          ? `<p class="mt-2 text-slate-600">Último evento: <span class="font-medium text-slate-800">${escapeHtml(st.last_event)}</span>${st.last_date ? ` · ${escapeHtml(dayLabel(st.last_date))} ${escapeHtml(timeOf(st.last_date))}` : ""}</p>`
          : "")
      : `<p class="text-slate-500">${state.statusLoaded ? "Sin información de rastreo." : "Consultando estado…"}</p>`;

    const pdfLink = $("od-pdf-link");
    if (pkg.label_url) {
      pdfLink.href = pkg.label_url;
      pdfLink.classList.remove("hidden");
    } else {
      pdfLink.removeAttribute("href");
      pdfLink.classList.add("hidden");
    }

    const t = $("wa-template");
    t.value = fillTemplate(TEMPLATE, data);
    t.dataset.loaded = "1";

    $("wa-phone").value = localStorage.getItem(SELLER_PHONE_KEY) || DEFAULT_SELLER_PHONE;
    updateLinks();

    $("drawer").classList.remove("hidden");
    document.body.classList.add("overflow-hidden");
    setTimeout(() => $("wa-template").focus({ preventScroll: true }), 50);
  }

  function closeDrawer() {
    $("drawer").classList.add("hidden");
    document.body.classList.remove("overflow-hidden");
  }

  function updateLinks() {
    const raw = $("wa-phone").value;
    localStorage.setItem(SELLER_PHONE_KEY, raw);
    const phone = raw.replace(/\D/g, "");
    const message = $("wa-template").value;
    const ready = phone.length >= 10 && $("wa-template").dataset.loaded === "1";

    const wa = $("wa-link");
    const tg = $("tg-link");
    [wa, tg].forEach((a) => {
      a.classList.toggle("hidden", !ready);
      if (!ready) a.removeAttribute("href");
    });
    if (!ready) return;

    const number = phone.length === 10 ? `57${phone}` : phone;
    // wa.me abre WhatsApp Web en el computador y la app en el celular.
    wa.href = `https://wa.me/${number}?text=${encodeURIComponent(message)}`;
    tg.href = TELEGRAM_CHAT_URL;
  }

  async function copyMessage() {
    const btn = $("btn-copy-msg");
    try {
      await navigator.clipboard.writeText($("wa-template").value);
      const idle = btn.innerHTML;
      btn.innerHTML = `<svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5 10 17 19 7"/></svg> Copiado`;
      btn.classList.add("!bg-emerald-50", "!text-emerald-700", "!ring-emerald-200");
      setTimeout(() => {
        btn.innerHTML = idle;
        btn.classList.remove("!bg-emerald-50", "!text-emerald-700", "!ring-emerald-200");
      }, 1800);
      window.toast("Mensaje copiado");
    } catch (err) {
      window.toast("No se pudo copiar. Selecciona el texto y cópialo a mano.", "error");
    }
  }

  // ---------- Vistas ----------

  function showView(name) {
    ["crear", "envios", "finanzas"].forEach((v) => {
      $(`view-${v}`).classList.toggle("hidden", v !== name);
      $(`tab-${v}`).setAttribute("aria-selected", String(v === name));
    });
    if (name === "envios" && !state.loaded) loadShipments();
    if (name === "finanzas" && window.finanzas) window.finanzas.show();
    history.replaceState(null, "", name === "crear" ? location.pathname : `#${name}`);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  // ---------- Eventos ----------

  $("tab-crear").addEventListener("click", () => showView("crear"));
  $("tab-envios").addEventListener("click", () => showView("envios"));
  $("tab-finanzas").addEventListener("click", () => showView("finanzas"));

  $("btn-refresh-orders").addEventListener("click", loadShipments);

  $("btn-filter-today").addEventListener("click", () => {
    state.filter = "today";
    $("filter-day").value = "";
    state.page = 1;
    render();
  });
  $("btn-filter-yesterday").addEventListener("click", () => {
    state.filter = "yesterday";
    $("filter-day").value = "";
    state.page = 1;
    render();
  });
  $("btn-filter-all").addEventListener("click", () => {
    state.filter = "all";
    $("filter-day").value = "";
    state.page = 1;
    render();
  });
  $("filter-estado").addEventListener("change", (e) => {
    state.estado = e.target.value;
    state.page = 1;
    render();
  });
  $("filter-cobro").addEventListener("change", (e) => {
    state.cobro = e.target.value;
    state.page = 1;
    render();
  });
  $("filter-day").addEventListener("change", () => {
    state.day = $("filter-day").value;
    state.filter = state.day ? "date" : "all";
    state.page = 1;
    render();
  });
  $("filter-search").addEventListener("input", (e) => {
    state.search = e.target.value;
    state.page = 1;
    render();
  });

  $("btn-prev-page").addEventListener("click", () => {
    state.page = Math.max(1, state.page - 1);
    render();
  });
  $("btn-next-page").addEventListener("click", () => {
    const pages = Math.max(1, Math.ceil(filtered().length / PAGE_SIZE));
    state.page = Math.min(pages, state.page + 1);
    render();
  });

  // Acciones de tabla y tarjetas (delegación)
  document.addEventListener("click", (e) => {
    const target = e.target.closest("[data-action]");
    if (!target) return;
    const { action } = target.dataset;
    if (action === "all") {
      $("btn-filter-all").click();
      return;
    }
    const shipment = state.items.find((s) => s.id === target.dataset.id);
    if (action === "pdf" && shipment) downloadLabel(shipment.id);
    if (action === "msg" && shipment) openDrawer(shipment);
  });

  $("btn-close-drawer").addEventListener("click", closeDrawer);
  $("drawer-overlay").addEventListener("click", closeDrawer);
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !$("drawer").classList.contains("hidden")) closeDrawer();
  });

  $("btn-copy-msg").addEventListener("click", copyMessage);
  $("wa-template").addEventListener("input", updateLinks);
  $("wa-phone").addEventListener("input", updateLinks);
  $("tg-link").addEventListener("click", () => {
    navigator.clipboard.writeText($("wa-template").value).catch(() => {});
    window.toast("Mensaje copiado. Pégalo en el chat de Telegram.", "info");
  });

  // Estado inicial: fecha de hoy en el selector.
  $("filter-day").max = todayBogota();
  render();
  renderLoading();
  // La pestaña se recuerda en la dirección (#envios, #finanzas) para poder volver directo.
  const initial = location.hash.slice(1);
  document.addEventListener("DOMContentLoaded", () => showView(["envios", "finanzas"].includes(initial) ? initial : "crear"));
  // Carga anticipada para que la pestaña abra con datos
  loadShipments();
})();
