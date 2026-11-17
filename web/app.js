(() => {
  let currentIdempotencyKey = null;

  const $ = (id) => document.getElementById(id);

  const screens = { 1: $("screen-1"), 2: $("screen-2"), 3: $("screen-3") };
  const steps = { 1: $("step-ind-1"), 2: $("step-ind-2"), 3: $("step-ind-3") };
  const CHECK_SVG = '<svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5 10 17 19 7"/></svg>';
  const SPINNER = '<span class="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white"></span>';

  // ---------- UI utilities ----------

  // Floating notification (used by both views).
  window.toast = function toast(message, type = "success") {
    const root = $("toast-root");
    const colors = {
      success: "bg-slate-900 text-white",
      error: "bg-red-600 text-white",
      info: "bg-indigo-600 text-white",
    };
    const icons = {
      success: "✓",
      error: "✕",
      info: "•",
    };
    const el = document.createElement("div");
    el.className = `toast-in pointer-events-auto flex max-w-sm items-center gap-3 rounded-xl px-4 py-3 text-sm font-medium shadow-xl ${colors[type] || colors.success}`;
    el.innerHTML = `<span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-white/15 text-xs">${icons[type] || icons.success}</span><span></span>`;
    el.lastChild.textContent = message;
    root.appendChild(el);
    setTimeout(() => {
      el.style.transition = "opacity .3s, transform .3s";
      el.style.opacity = "0";
      el.style.transform = "translateY(6px)";
      setTimeout(() => el.remove(), 300);
    }, 2600);
  };

  function showBox(el, message) {
    el.textContent = message;
    el.classList.remove("hidden");
  }

  function hideBox(el) {
    el.classList.add("hidden");
  }

  // Coverage box styled according to the result.
  const coverageBox = $("coverage-status");
  const COVERAGE_STYLES = {
    loading: "border-slate-200 bg-slate-50 text-slate-600",
    ok: "border-emerald-200 bg-emerald-50 text-emerald-800",
    error: "border-red-200 bg-red-50 text-red-700",
  };
  function setCoverage(type, html) {
    coverageBox.className = `rounded-xl border px-4 py-3 text-sm ${COVERAGE_STYLES[type]}`;
    coverageBox.innerHTML = html;
  }

  // Step indicator: marks the current step and moves the progress bar.
  function setStepper(num) {
    [1, 2, 3].forEach((n) => {
      const state = n < num ? "done" : n === num ? "current" : "todo";
      steps[n].dataset.state = state;
      const dot = steps[n].querySelector(".step-dot");
      dot.innerHTML = state === "done" ? CHECK_SVG : String(n);
    });
    $("progress-bar").style.width = `${((num - 1) / 2) * 100}%`;
  }

  function showScreen(num) {
    Object.keys(screens).forEach((k) => {
      const on = Number(k) === num;
      screens[k].classList.toggle("hidden", !on);
      if (on) {
        screens[k].classList.remove("step-enter");
        void screens[k].offsetWidth; // restarts the animation
        screens[k].classList.add("step-enter");
      }
    });
    setStepper(num);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function setLoading(btn, loadingHtml, loading, idleHtml) {
    btn.disabled = loading;
    btn.innerHTML = loading ? loadingHtml : idleHtml;
  }

  function collectFormData() {
    return {
      nombre: $("f-nombre").value.trim(),
      documento: $("f-documento").value.trim(),
      telefono: $("f-telefono").value.trim(),
      correo: $("f-correo").value.trim(),
      pais: $("f-pais").value.trim() || "Colombia",
      departamento: $("f-departamento").value.trim(),
      ciudad: $("f-ciudad").value.trim(),
      barrio: $("f-barrio").value.trim(),
      direccion: $("f-direccion").value.trim(),
      cantidad: parseInt($("f-cantidad").value, 10) || 1,
      valor_unitario: parseFloat($("f-valor_unitario").value) || 0,
      referencia: $("f-referencia").value.trim(),
      codigo_postal: $("f-codigo-postal").value.trim(),
    };
  }

  function generateUUID() {
    if (crypto && crypto.randomUUID) return crypto.randomUUID();
    return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
      const r = (Math.random() * 16) | 0;
      const v = c === "x" ? r : (r & 0x3) | 0x8;
      return v.toString(16);
    });
  }

  // ---------- Server status ----------

  fetch("/api/health")
    .then((r) => r.json())
    .then((info) => {
      const live = info.mode === "live";
      $("system-badge").textContent = `${info.origin} · ${info.carrier} · ${live ? "Producción" : "Simulación"}`;
    })
    .catch(() => {
      $("system-badge").textContent = "Sin conexión con el backend";
    });

  // ---------- Step 1: paste and process ----------

  $("btn-load-sample").addEventListener("click", () => {
    $("raw-message").value = `¡Gracias Yeison! Ya quedó todo anotado 🫡
DATOS DE ENVÍO – SERVICIO TÉCNICO ISTMINA
Receptor: YEISON ROA
Documento: 1077458155
Teléfono: 3145615105
Ciudad: QUIBDO CHOCO
Barrio: cesar contó
Dirección: Carrera 7 con 30
Correo electrónico: yeisoncuesta27@gmail.com
Cantidad: 2

ESTADO: Pendiente de despacho
SERVICIO TÉCNICO ISTMINA`;
    hideBox($("error-screen-1"));
  });

  $("btn-parse").addEventListener("click", async () => {
    const raw = $("raw-message").value;
    const errBox = $("error-screen-1");
    hideBox(errBox);

    if (!raw.trim()) {
      showBox(errBox, "Pega el mensaje de WhatsApp antes de continuar.");
      return;
    }

    const btn = $("btn-parse");
    const idle = btn.innerHTML;
    setLoading(btn, `${SPINNER} Procesando…`, true, idle);

    try {
      const res = await fetch("/api/messages/parse", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ raw_message: raw }),
      });
      const payload = await res.json();
      if (!res.ok) throw new Error(payload.error || "Error interpretando el mensaje");

      populateForm(payload);
      currentIdempotencyKey = generateUUID();
      showScreen(2);
    } catch (err) {
      showBox(errBox, err.message);
    } finally {
      setLoading(btn, "", false, idle);
    }
  });

  function populateForm(parsed) {
    const d = parsed.data;
    $("f-nombre").value = d.nombre || "";
    $("f-documento").value = d.documento || "";
    $("f-telefono").value = d.telefono || "";
    $("f-correo").value = d.correo || "";
    $("f-pais").value = d.pais || "Colombia";
    $("f-departamento").value = d.departamento || "";
    $("f-ciudad").value = d.ciudad || "";
    $("f-barrio").value = d.barrio || "";
    $("f-direccion").value = d.direccion || "";
    $("f-cantidad").value = d.cantidad || 1;
    $("f-referencia").value = d.referencia || "";
    $("f-codigo-postal").value = d.codigo_postal || "";

    document.querySelectorAll("#shipment-form input").forEach((inp) => inp.classList.remove("input-missing"));
    hideBox($("error-screen-2"));
    setCoverage("loading", "");
    coverageBox.classList.add("hidden");

    const notes = [];
    // The unit price always has a default value in the form; it isn't missing data from the message.
    const missing = (parsed.missing_fields || []).filter((f) => !f.startsWith("valor_unitario"));
    if (missing.length > 0) {
      notes.push(`Revisa estos campos: ${missing.join(", ")}.`);
      missing.forEach((field) => {
        const el = $(`f-${field.split(" ")[0]}`);
        if (el) el.classList.add("input-missing");
      });
    }
    if (parsed.warnings && parsed.warnings.length > 0) notes.push(...parsed.warnings);

    if (notes.length > 0) showBox($("alert-missing"), notes.join(" "));
    else hideBox($("alert-missing"));
  }

  // ---------- Step 2: review, validate and confirm ----------

  $("btn-back").addEventListener("click", () => showScreen(1));

  // Live coverage validation: quotes the route in Skydropx without creating anything.
  $("btn-validate-address").addEventListener("click", async () => {
    const btn = $("btn-validate-address");
    const idle = btn.innerHTML;
    setLoading(btn, `${SPINNER} Validando…`, true, idle);
    coverageBox.classList.remove("hidden");
    setCoverage("loading", `${SPINNER.replace("border-white/40 border-t-white", "border-slate-300 border-t-indigo-600")} <span class="ml-2">Consultando cobertura en Skydropx…</span>`);

    try {
      const res = await fetch("/api/validate-address", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ data: collectFormData() }),
      });
      const payload = await res.json();
      if (!res.ok) throw new Error(payload.error || "No se pudo validar la dirección");

      if (payload.ok) {
        const chips = (payload.carriers || [])
          .slice(0, 5)
          .map((c) => `<span class="badge bg-white text-emerald-700 ring-1 ring-emerald-200">${escapeHtml(c)}</span>`)
          .join(" ");
        setCoverage("ok", `<p class="font-semibold">✓ ${escapeHtml(payload.message)}</p><div class="mt-2 flex flex-wrap gap-1.5">${chips}</div>`);
      } else {
        setCoverage("error", `<p class="font-semibold">✕ Sin cobertura</p><p class="mt-1">${escapeHtml(payload.message)}</p>`);
      }
    } catch (err) {
      setCoverage("error", `<p class="font-semibold">✕ ${escapeHtml(err.message)}</p>`);
    } finally {
      setLoading(btn, "", false, idle);
    }
  });

  $("shipment-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    hideBox($("error-screen-2"));

    const btn = $("btn-submit");
    const idle = btn.innerHTML;
    setLoading(btn, `${SPINNER} Generando guía en Skydropx…`, true, idle);

    try {
      const res = await fetch("/api/shipments", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": currentIdempotencyKey,
        },
        body: JSON.stringify({
          idempotency_key: currentIdempotencyKey,
          data: collectFormData(),
        }),
      });
      const payload = await res.json();

      if (res.status === 422) {
        showBox($("error-screen-2"), `${payload.error}: ${(payload.missing_fields || []).join(", ")}`);
        return;
      }
      if (!res.ok) {
        showErrorResult(payload.error || "Error inesperado al comunicarse con Skydropx");
        return;
      }
      showSuccessResult(payload);
    } catch (err) {
      showErrorResult(err.message);
    } finally {
      setLoading(btn, "", false, idle);
    }
  });

  // ---------- Step 3: result ----------

  function showSuccessResult(res) {
    $("result-success").classList.remove("hidden");
    $("result-error").classList.add("hidden");

    $("res-nombre").textContent = res.recipient_name;
    $("res-destino").textContent = res.destination;
    $("res-carrier").textContent = res.carrier;
    $("res-tracking").textContent = res.tracking_number || "Pendiente de asignación";

    const costRow = $("row-cost");
    if (res.estimated_cost) {
      $("res-cost").textContent = res.estimated_cost;
      costRow.classList.remove("hidden");
    } else {
      costRow.classList.add("hidden");
    }

    $("mock-badge").classList.toggle("hidden", !res.is_mock);

    const btnLabel = $("btn-download-label");
    if (res.label_url) {
      btnLabel.href = res.label_url;
      btnLabel.classList.remove("hidden");
    } else {
      btnLabel.classList.add("hidden");
    }

    showScreen(3);
    window.toast("Envío generado correctamente");
  }

  function showErrorResult(msg) {
    $("result-success").classList.add("hidden");
    $("result-error").classList.remove("hidden");
    $("res-error-msg").textContent = msg;
    showScreen(3);
  }

  $("btn-retry").addEventListener("click", () => showScreen(2));

  $("btn-new-shipment").addEventListener("click", () => {
    $("raw-message").value = "";
    currentIdempotencyKey = null;
    showScreen(1);
  });

  function escapeHtml(text) {
    return String(text ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  setStepper(1);
})();
