"use strict";
/* ------------------------------------------------------------------ *
 *  Minimal MCP Apps bridge — JSON-RPC 2.0 over postMessage.
 *  Spec: modelcontextprotocol/ext-apps specification/2026-01-26.
 * ------------------------------------------------------------------ */
const bridge = (() => {
  let nextID = 1;
  const pending = new Map();
  const notificationHandlers = new Map();

  function post(msg) { window.parent.postMessage(msg, "*"); }

  function request(method, params) {
    return new Promise((resolve, reject) => {
      const id = nextID++;
      pending.set(id, { resolve, reject });
      post({ jsonrpc: "2.0", id, method, params: params || {} });
    });
  }

  function notify(method, params) {
    post({ jsonrpc: "2.0", method, params: params || {} });
  }

  window.addEventListener("message", (event) => {
    const msg = event.data;
    if (!msg || typeof msg !== "object" || msg.jsonrpc !== "2.0") return;
    if (msg.id !== undefined && (msg.result !== undefined || msg.error !== undefined)) {
      const p = pending.get(msg.id);
      if (p) {
        pending.delete(msg.id);
        if (msg.error) p.reject(new Error(msg.error.message || "host error"));
        else p.resolve(msg.result);
      }
      return;
    }
    if (msg.method) {
      const handler = notificationHandlers.get(msg.method);
      if (handler) handler(msg.params || {});
    }
  });

  return { request, notify, on: (m, fn) => notificationHandlers.set(m, fn) };
})();

/* ------------------------------------------------------------------ *
 *  DOM helpers — data is third-party; only ever assign textContent.
 * ------------------------------------------------------------------ */
const root = document.getElementById("root");
function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function clearRoot() { root.textContent = ""; }
function toast(message) {
  const t = el("div", "toast", message);
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 3200);
}
function imageOr(urlString, fallbackTitle, host) {
  if (urlString && /^https:\/\//.test(urlString)) {
    const img = document.createElement("img");
    img.loading = "lazy";
    img.alt = "";
    img.src = urlString;
    img.onerror = () => { host.textContent = ""; host.appendChild(placeholderFor(fallbackTitle)); };
    return img;
  }
  return placeholderFor(fallbackTitle);
}
function placeholderFor(title) {
  const initial = (title || "?").trim().charAt(0).toUpperCase() || "?";
  return el("div", "thumb-fallback", initial);
}
function domainOf(urlString) {
  try { return new URL(urlString).hostname.replace(/^www\./, ""); } catch { return ""; }
}
function qtyText(ing) {
  if (ing.original_text) return "";
  let amount = ing.amount;
  if (!amount) return "";
  let text = String(Math.round(amount * 100) / 100);
  if (ing.amount_high) text += "–" + String(Math.round(ing.amount_high * 100) / 100);
  if (ing.unit) text += " " + ing.unit;
  return text;
}
function brandline(kicker, showBack, onBack) {
  const bar = el("div", "brandline");
  bar.appendChild(el("div", "brand-mark", "Sb"));
  bar.appendChild(el("span", "kicker", kicker));
  bar.appendChild(el("div", "spacer"));
  if (showBack) {
    const back = el("button", "backbtn", "← Back");
    back.onclick = onBack;
    bar.appendChild(back);
  }
  root.appendChild(bar);
}
function skeleton() {
  clearRoot();
  brandline("SaltyBytes", false);
  const wrap = el("div", "skeleton");
  for (let i = 0; i < 3; i++) wrap.appendChild(el("div", "sk"));
  root.appendChild(wrap);
}

/* ------------------------------------------------------------------ *
 *  Views
 * ------------------------------------------------------------------ */
let lastListView = null; // for back navigation from a detail card

function render(data) {
  if (!data || typeof data !== "object" || !data.view) return;
  switch (data.view) {
    case "search_results": lastListView = data; renderSearch(data); break;
    case "recipe_list": lastListView = data; renderMyRecipes(data); break;
    case "preview": renderPreview(data); break;
    case "recipe_card": renderRecipeCard(data); break;
  }
}

function renderSearch(data) {
  clearRoot();
  brandline("Recipes from around the web", false);
  const results = data.results || [];
  if (!results.length) {
    root.appendChild(el("div", "note", "No recipes found — try different words."));
    return;
  }
  const grid = el("div", "grid");
  results.forEach((r) => {
    const card = el("article", "rcard");
    const thumb = el("div", "thumb");
    thumb.appendChild(imageOr(r.image_url, r.title, thumb));
    card.appendChild(thumb);
    const body = el("div", "body");
    body.appendChild(el("div", "src", r.source_domain || domainOf(r.source_url)));
    body.appendChild(el("h3", "", r.title || "Untitled recipe"));
    const meta = el("div", "meta");
    if (r.rating) {
      meta.appendChild(el("span", "star", "★"));
      meta.appendChild(el("span", "", String(Math.round(r.rating * 10) / 10)));
    }
    body.appendChild(meta);
    card.appendChild(body);
    card.onclick = () => openPreview(r.source_url);
    grid.appendChild(card);
  });
  root.appendChild(grid);
}

function renderMyRecipes(data) {
  clearRoot();
  brandline("Your saved recipes", false);
  const recipes = data.recipes || [];
  if (!recipes.length) {
    root.appendChild(el("div", "note", "Nothing saved yet — search for a recipe and give it a home."));
    return;
  }
  const grid = el("div", "grid");
  recipes.forEach((r) => {
    const card = el("article", "rcard");
    const thumb = el("div", "thumb");
    thumb.appendChild(imageOr(r.imageUrl, r.title, thumb));
    card.appendChild(thumb);
    const body = el("div", "body");
    body.appendChild(el("div", "src", (r.tags && r.tags[0]) || "saved"));
    body.appendChild(el("h3", "", r.title || "Untitled recipe"));
    const meta = el("div", "meta");
    if (r.cookTimeMinutes) meta.appendChild(el("span", "", "⏱ " + r.cookTimeMinutes + " min"));
    body.appendChild(meta);
    card.appendChild(body);
    card.onclick = () => openSaved(r.id);
    grid.appendChild(card);
  });
  root.appendChild(grid);
  if (data.total > recipes.length) {
    root.appendChild(el("div", "pinch", "∴"));
    root.appendChild(el("div", "note", recipes.length + " of " + data.total + " — ask for more, or search your collection."));
  }
}

function renderPreview(data) {
  if (data.is_multi) { renderMulti(data); return; }
  const recipe = data.recipe;
  if (!recipe) { renderEmpty("Couldn't read a recipe from that page."); return; }
  renderDetail({
    title: recipe.title,
    imageURL: "",
    cookTime: recipe.cook_time,
    portions: recipe.portions,
    ingredients: recipe.ingredients || [],
    instructions: recipe.instructions || [],
    sourceURL: recipe.source_url || data.source_url,
    saved: false,
    saveURL: data.source_url,
  });
}

function renderRecipeCard(data) {
  const recipe = data.recipe;
  if (!recipe) { renderEmpty("Recipe not found."); return; }
  renderDetail({
    title: recipe.title,
    imageURL: recipe.imageUrl,
    cookTime: recipe.cookTimeMinutes,
    portions: 0,
    ingredients: recipe.ingredients || [],
    instructions: recipe.instructions || [],
    sourceURL: recipe.sourceUrl,
    saved: !!data.saved,
    saveURL: "",
  });
}

function renderMulti(data) {
  clearRoot();
  brandline("This page has several recipes", !!lastListView, () => render(lastListView));
  (data.recipes || []).forEach((card, i) => {
    const row = el("div", "multirow");
    row.appendChild(el("div", "n", String(i + 1)));
    const col = el("div", "");
    col.appendChild(el("div", "t", card.title || "Untitled"));
    if (card.description) col.appendChild(el("div", "d", card.description));
    row.appendChild(col);
    row.onclick = () => openPreview(card.cached_url || card.source_url);
    root.appendChild(row);
  });
}

function renderDetail(view) {
  clearRoot();
  brandline(view.saved ? "From your collection" : "Recipe preview", !!lastListView, () => render(lastListView));

  const detail = el("div", "detail");
  const hero = el("div", "hero");
  if (view.imageURL) {
    const photo = el("div", "photo");
    photo.appendChild(imageOr(view.imageURL, view.title, photo));
    hero.appendChild(photo);
  }
  const headings = el("div", "headings");
  headings.appendChild(el("h2", "", view.title || "Untitled recipe"));
  const chips = el("div", "chips");
  if (view.cookTime) chips.appendChild(el("span", "chip accent", "⏱ " + view.cookTime + " min"));
  if (view.portions) chips.appendChild(el("span", "chip", "serves " + view.portions));
  chips.appendChild(el("span", "chip", view.ingredients.length + " ingredients"));
  headings.appendChild(chips);

  const actions = el("div", "actions");
  if (view.saved) {
    actions.appendChild(makeSavedBadge());
  } else if (view.saveURL) {
    const save = el("button", "savebtn", "Save to SaltyBytes");
    save.onclick = () => saveRecipe(save, actions, view.saveURL);
    actions.appendChild(save);
  }
  if (view.sourceURL) {
    const link = el("button", "linkbtn", domainOf(view.sourceURL) || "source");
    link.onclick = () => openLink(view.sourceURL);
    actions.appendChild(link);
  }
  headings.appendChild(actions);
  hero.appendChild(headings);
  detail.appendChild(hero);
  detail.appendChild(el("div", "pinch", "∴"));

  const cols = el("div", "cols");
  const ingCol = el("div", "");
  ingCol.appendChild(el("div", "sect-label", "Ingredients"));
  const ingList = el("ul", "ing");
  view.ingredients.forEach((ing) => {
    const li = el("li", "");
    li.appendChild(el("span", "box", ""));
    const txt = el("span", "txt", "");
    const qty = qtyText(ing);
    if (qty) {
      txt.appendChild(el("span", "qty", qty + " "));
      txt.appendChild(document.createTextNode(ing.name || ""));
    } else {
      txt.textContent = ing.original_text || ing.name || "";
    }
    li.appendChild(txt);
    li.onclick = () => li.classList.toggle("done");
    ingList.appendChild(li);
  });
  ingCol.appendChild(ingList);
  cols.appendChild(ingCol);

  const stepCol = el("div", "");
  stepCol.appendChild(el("div", "sect-label", "Method"));
  const steps = el("ol", "steps");
  view.instructions.forEach((step) => steps.appendChild(el("li", "", step)));
  stepCol.appendChild(steps);
  cols.appendChild(stepCol);

  detail.appendChild(cols);
  root.appendChild(detail);
}

function makeSavedBadge() {
  const badge = el("span", "savedbadge");
  badge.appendChild(el("span", "", "✓"));
  badge.appendChild(el("span", "", "In your collection"));
  return badge;
}

function renderEmpty(message) {
  clearRoot();
  brandline("SaltyBytes", !!lastListView, () => render(lastListView));
  root.appendChild(el("div", "note", message));
}

/* ------------------------------------------------------------------ *
 *  Tool calls initiated from the widget
 * ------------------------------------------------------------------ */
async function callTool(name, args) {
  const oa = openAIHost();
  if (oa && typeof oa.callTool === "function") {
    // ChatGPT Apps runtime: returns { structuredContent, content }.
    const next = await oa.callTool(name, args);
    return (next && next.structuredContent) || {};
  }
  const result = await bridge.request("tools/call", { name, arguments: args });
  if (result && result.isError) {
    const text = (result.content || []).map((c) => c.text).filter(Boolean).join(" ");
    throw new Error(text || "That didn't work — try again.");
  }
  return (result && result.structuredContent) || {};
}

// openLink sends the user to an external URL via whichever host bridge is active.
function openLink(url) {
  if (!url) return;
  const oa = openAIHost();
  if (oa && typeof oa.openExternal === "function") {
    try { oa.openExternal({ href: url }); return; } catch (e) { /* fall through */ }
    try { oa.openExternal(url); return; } catch (e) { /* fall through */ }
  }
  bridge.request("ui/open-link", { url }).catch(() => {});
}

async function openPreview(url) {
  if (!url) return;
  skeleton();
  try { render(await callTool("preview_recipe", { url })); }
  catch (err) { renderEmpty(err.message); }
}

async function openSaved(id) {
  if (!id) return;
  skeleton();
  try { render(await callTool("get_recipe", { recipe_id: String(id) })); }
  catch (err) { renderEmpty(err.message); }
}

async function saveRecipe(button, actions, url) {
  button.disabled = true;
  button.textContent = "Saving…";
  try {
    await callTool("save_recipe", { url });
    button.classList.add("saved");
    button.textContent = "✓ Saved to SaltyBytes";
    toast("Saved — it's waiting in your app.");
  } catch (err) {
    button.disabled = false;
    button.textContent = "Save to SaltyBytes";
    toast(err.message);
  }
}

/* ------------------------------------------------------------------ *
 *  Lifecycle: initialize, receive tool results, report size.
 * ------------------------------------------------------------------ */
function applyHostContext(hostContext) {
  if (!hostContext) return;
  if (hostContext.theme === "dark" || hostContext.theme === "light") {
    document.documentElement.setAttribute("data-theme", hostContext.theme);
  }
}

// openAIHost returns ChatGPT's Apps runtime object when the widget is embedded in
// ChatGPT, else null. ChatGPT delivers the tool's structured output via
// window.openai (globals + the "openai:set_globals" event), NOT the MCP Apps
// postMessage bridge — so without this the widget would sit on its loading note
// in ChatGPT forever.
function openAIHost() {
  return (typeof window !== "undefined" && window.openai) ? window.openai : null;
}

// applyOpenAIGlobals renders from the ChatGPT runtime globals: theme + the tool's
// structured output, which is exactly the shape render() expects.
function applyOpenAIGlobals(globals) {
  if (!globals) return;
  if (globals.theme === "dark" || globals.theme === "light") {
    document.documentElement.setAttribute("data-theme", globals.theme);
  }
  if (globals.toolOutput) render(globals.toolOutput);
}

// MCP Apps postMessage host (Claude and other ext-apps hosts).
bridge.on("ui/notifications/tool-result", (params) => {
  if (params && params.structuredContent) render(params.structuredContent);
});
bridge.on("ui/notifications/tool-input", () => { skeleton(); });
bridge.on("ui/notifications/host-context-changed", (params) => {
  applyHostContext(params && params.hostContext);
});

// ChatGPT Apps runtime host: tool output + theme arrive via window.openai globals.
window.addEventListener("openai:set_globals", (event) => {
  applyOpenAIGlobals(event && event.detail && event.detail.globals);
});

const resizeObserver = new ResizeObserver(() => {
  bridge.notify("ui/notifications/size-changed", {
    width: document.documentElement.scrollWidth,
    height: document.documentElement.scrollHeight,
  });
});

(function main() {
  // ChatGPT: window.openai is present at mount — render immediately from whatever
  // is already set, then let "openai:set_globals" drive updates.
  const oa = openAIHost();
  if (oa) applyOpenAIGlobals(oa);
  // MCP Apps host (Claude, etc.): run the postMessage handshake, but NON-BLOCKING
  // so a host that never answers ui/initialize (ChatGPT) can't leave the widget
  // stuck on its loading note.
  bridge.request("ui/initialize", {
    protocolVersion: "2026-01-26",
    clientInfo: { name: "saltybytes-widget", version: "1.0.0" },
    capabilities: {},
  }).then((init) => {
    applyHostContext(init && init.hostContext);
    bridge.notify("ui/notifications/initialized");
  }).catch(() => { /* not an MCP Apps postMessage host */ });
  resizeObserver.observe(document.body);
})();

// ChatGPT can set window.openai.toolOutput a tick after mount without firing
// "openai:set_globals" — poll briefly so we don't miss a late delivery. If nothing
// has rendered after a few seconds, surface what the host actually exposes instead
// of sitting on the loading note. (Diagnostic string removed once ChatGPT render
// is confirmed.)
(function watchOpenAI() {
  let n = 0;
  const timer = setInterval(() => {
    n++;
    const oa = openAIHost();
    if (oa && oa.toolOutput) { clearInterval(timer); applyOpenAIGlobals(oa); return; }
    if (n >= 24) {
      clearInterval(timer);
      if (root.textContent.indexOf("Warming up") !== -1) {
        const o = openAIHost();
        const info = o
          ? "openai present; keys: " + Object.keys(o).slice(0, 14).join(", ") + "; toolOutput " + (o.toolOutput ? "set" : "absent")
          : "window.openai ABSENT in this iframe";
        clearRoot();
        root.appendChild(el("div", "note", "SaltyBytes debug — " + info));
      }
    }
  }, 250);
})();
