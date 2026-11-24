// Soft Serve's issue views: keyboard shortcuts, menus that change an issue
// in place, and dragging cards on the board. Every page works without this
// script; it only saves trips through forms and reloads.
(() => {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  // --- Talking to the server ---

  // say puts a message in a live region, so it's read out and seen.
  const say = (text, bad) => {
    let box = $("#issue-status");
    if (!box) {
      box = document.createElement("p");
      box.id = "issue-status";
      box.setAttribute("role", "status");
      document.body.append(box);
    }
    box.textContent = text;
    box.className = bad ? "toast bad" : "toast";
    clearTimeout(say.timer);
    say.timer = setTimeout(() => { box.textContent = ""; box.className = ""; }, bad ? 6000 : 2500);
  };

  // post sends a form's fields, as the form would, and says why if the
  // server refuses.
  const post = async (url, fields) => {
    const body = new URLSearchParams(fields);
    const res = await fetch(url, {
      method: "POST",
      body,
      headers: { "X-Requested-With": "soft-serve" },
      credentials: "same-origin",
    });
    if (!res.ok) {
      let msg = "That didn't work.";
      try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
      throw new Error(msg);
    }
  };

  // refresh fetches the page again and swaps in its main content, keeping
  // focus on the same issue where it can.
  const refresh = async () => {
    const focusKey = document.activeElement?.closest("[data-issue]")?.dataset.issue;
    const res = await fetch(location.href, { credentials: "same-origin" });
    if (!res.ok) { location.reload(); return; }
    const doc = new DOMParser().parseFromString(await res.text(), "text/html");
    const fresh = $("main", doc);
    if (!fresh) { location.reload(); return; }
    $("main").replaceWith(fresh);
    setup();
    if (focusKey) $(`[data-issue="${CSS.escape(focusKey)}"] .ititle`)?.focus({ preventScroll: true });
  };

  // --- Menus: send in place ---

  const menus = () => {
    for (const form of $$(".qmenu form")) {
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        const btn = e.submitter;
        const fields = new FormData(form);
        if (btn?.name) fields.set(btn.name, btn.value);
        fields.delete("back");
        form.closest("details")?.removeAttribute("open");
        try {
          await post(form.action, fields);
          await refresh();
          say(btn ? `Set to ${btn.textContent.trim()}.` : "Saved.");
        } catch (err) {
          say(err.message, true);
        }
      });
    }
  };

  // One menu open at a time; a click elsewhere or Escape closes it.
  const closeMenus = (except) => {
    for (const d of $$("details.qmenu[open]")) if (d !== except) d.removeAttribute("open");
  };
  document.addEventListener("toggle", (e) => {
    const d = e.target;
    if (d.matches?.("details.qmenu") && d.open) {
      closeMenus(d);
      (d.querySelector('[aria-current="true"]') || d.querySelector("button"))?.focus();
    }
  }, true);
  document.addEventListener("click", (e) => {
    if (!e.target.closest("details.qmenu")) closeMenus();
  });

  // Arrow keys move through an open menu.
  document.addEventListener("keydown", (e) => {
    const menu = e.target.closest?.("details.qmenu[open]");
    if (!menu || !["ArrowDown", "ArrowUp"].includes(e.key)) return;
    const items = $$("button", menu);
    const i = items.indexOf(document.activeElement);
    const next = items[(i + (e.key === "ArrowDown" ? 1 : -1) + items.length) % items.length];
    next?.focus();
    e.preventDefault();
  });

  // --- The board: drag cards between and within columns ---

  const board = () => {
    const root = $(".board");
    if (!root || !matchMedia("(pointer: fine)").matches) return;
    const canWrite = !!$(".bcard .qmenu", root);
    if (!canWrite) return;
    let dragged = null;
    const marker = document.createElement("li");
    marker.className = "bdrop";
    marker.setAttribute("aria-hidden", "true");

    for (const card of $$(".bcard", root)) {
      card.draggable = true;
      card.addEventListener("dragstart", (e) => {
        dragged = card;
        card.classList.add("dragging");
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", card.dataset.issue);
      });
      card.addEventListener("dragend", () => {
        card.classList.remove("dragging");
        marker.remove();
        dragged = null;
      });
    }
    // Where a card would land: before the first card whose middle is
    // below the pointer.
    const landing = (list, y) => {
      const cards = $$(".bcard:not(.dragging)", list);
      return cards.find((c) => {
        const r = c.getBoundingClientRect();
        return y < r.top + r.height / 2;
      }) || null;
    };
    for (const col of $$(".bcol", root)) {
      const list = $(".bcards", col);
      col.addEventListener("dragover", (e) => {
        if (!dragged) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        const before = landing(list, e.clientY);
        if (before) list.insertBefore(marker, before); else list.append(marker);
        col.classList.add("over");
      });
      col.addEventListener("dragleave", (e) => {
        if (!col.contains(e.relatedTarget)) col.classList.remove("over");
      });
      col.addEventListener("drop", async (e) => {
        e.preventDefault();
        col.classList.remove("over");
        if (!dragged) return;
        const card = dragged;
        // The card it lands after, not counting itself; none is the top.
        let prev = marker.previousElementSibling;
        while (prev && (prev === card || !prev.classList.contains("bcard"))) prev = prev.previousElementSibling;
        marker.replaceWith(card);
        const fields = { status: col.dataset.status, after: prev ? prev.dataset.issue : "top" };
        try {
          await post(card.dataset.quick, fields);
          await refresh();
          say(`Moved to ${$(".bcol-head span", col).textContent}.`);
        } catch (err) {
          say(err.message, true);
          await refresh();
        }
      });
    }
  };

  // --- Keyboard ---

  const typing = (el) => el && (el.isContentEditable || /^(input|textarea|select)$/i.test(el.tagName));
  const rows = () => $$(".irow .ititle, .bcard .ititle");
  const go = (href) => { if (href) location.href = href; };
  const viewLink = (name) => $(`.viewnav a[href$="/-/issues${name}"]`)?.href;
  let pendingG = false;

  const shortcuts = () => {
    const panel = $("#shortcuts");
    if (panel) panel.hidden = false;
  };

  document.addEventListener("keydown", (e) => {
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "Escape") {
      if ($("details.qmenu[open]")) { closeMenus(); e.preventDefault(); return; }
      if (typing(document.activeElement)) document.activeElement.blur();
      return;
    }
    if (typing(e.target)) return;

    if (pendingG) {
      pendingG = false;
      const to = { i: "", b: "/board", p: "/projects", y: "/cycles" }[e.key];
      if (to !== undefined) { go(viewLink(to)); e.preventDefault(); }
      return;
    }
    const focused = document.activeElement?.closest("[data-issue]");
    switch (e.key) {
      case "g":
        pendingG = true;
        setTimeout(() => { pendingG = false; }, 1200);
        return;
      case "?": {
        const panel = $("#shortcuts");
        if (panel) { panel.open = !panel.open; if (panel.open) panel.scrollIntoView({ block: "nearest" }); }
        break;
      }
      case "/":
        $('.issuefilter input[type="search"]')?.focus();
        break;
      case "c": {
        const a = $('a.button[href*="/-/issues/new"], .issuebar a.button');
        if (a) go(a.href); else return;
        break;
      }
      case "j":
      case "k": {
        const list = rows();
        if (!list.length) return;
        const i = list.indexOf(document.activeElement);
        const next = i < 0 ? (e.key === "j" ? 0 : list.length - 1) : Math.min(list.length - 1, Math.max(0, i + (e.key === "j" ? 1 : -1)));
        list[next].focus();
        list[next].scrollIntoView({ block: "nearest" });
        break;
      }
      case "s":
      case "p": {
        const which = e.key === "s" ? ".istat" : ".ipri";
        const menu = focused?.querySelector(`details.qmenu > summary${which}`)?.parentElement;
        if (menu) { menu.open = true; break; }
        // On an issue's page: open its property form at that field.
        const props = $("details.editprops");
        const field = $(`details.editprops select[name="${e.key === "s" ? "status" : "priority"}"]`);
        if (props && field) { props.open = true; field.focus(); break; }
        return;
      }
      case "a": {
        const props = $("details.editprops");
        const field = $('details.editprops input[name="assignee"]');
        if (props && field) { props.open = true; field.focus(); break; }
        return;
      }
      case "e": {
        const a = $('.issuehead a.button[href$="/edit"]');
        if (a) go(a.href); else return;
        break;
      }
      case "r": {
        const box = $('.commentform textarea');
        if (box) box.focus(); else return;
        break;
      }
      default:
        return;
    }
    e.preventDefault();
  });

  const setup = () => {
    menus();
    board();
    shortcuts();
  };
  setup();
})();
