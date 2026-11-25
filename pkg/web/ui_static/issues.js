// Soft Serve's issue views: keyboard shortcuts, menus that change an issue
// in place, and dragging cards on the board. Every page works without this
// script; it only saves trips through forms and reloads.
(() => {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  // --- Talking to the server ---

  // say puts a message in a live region, so it's read out and seen. With
  // undo, it offers a button that takes the change back, and stays longer.
  const say = (text, bad, undo) => {
    let box = $("#issue-status");
    if (!box) {
      box = document.createElement("p");
      box.id = "issue-status";
      box.setAttribute("role", "status");
      document.body.append(box);
    }
    box.textContent = text;
    box.className = bad ? "toast bad" : "toast";
    if (undo) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "link";
      btn.textContent = "Undo";
      btn.addEventListener("click", () => { box.textContent = ""; box.className = ""; undo(); });
      box.append(" ", btn);
    }
    clearTimeout(say.timer);
    say.timer = setTimeout(() => {
      if (box.contains(document.activeElement)) return;
      box.textContent = ""; box.className = "";
    }, bad || undo ? 8000 : 4000);
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

  // focusOf notes where el is, to find its like again after a refresh:
  // its issue's row, or for a menu outside any row, as in an issue's side
  // panel, the menu by its place on the page.
  const focusOf = (el) => ({
    focusKey: el?.closest("[data-issue]")?.dataset.issue,
    menuAt: $$("details.qmenu").indexOf(el?.closest("details.qmenu")),
  });

  // refresh fetches the page again and swaps in its main content, keeping
  // focus on the same issue where it can. where says what had the focus;
  // by default, whatever has it now.
  const refresh = async (where = focusOf(document.activeElement)) => {
    const { focusKey, menuAt } = where;
    // Text typed but not sent, such as a comment, survives the swap.
    const typed = $$("main textarea, main input[type=text]")
      .filter((el) => el.name && el.form && el.value !== el.defaultValue)
      .map((el) => [el.form.getAttribute("action"), el.name, el.value]);
    const res = await fetch(location.href, { credentials: "same-origin" });
    if (!res.ok) { location.reload(); return; }
    const doc = new DOMParser().parseFromString(await res.text(), "text/html");
    const fresh = $("main", doc);
    if (!fresh) { location.reload(); return; }
    $("main").replaceWith(fresh);
    for (const [action, name, value] of typed) {
      const el = $$(`main [name="${CSS.escape(name)}"]`).find((f) => f.form?.getAttribute("action") === action);
      if (el) el.value = value;
    }
    setup();
    if (focusKey) $(`[data-issue="${CSS.escape(focusKey)}"] .ititle`)?.focus({ preventScroll: true });
    else if (menuAt >= 0) $$("details.qmenu")[menuAt]?.querySelector("summary")?.focus({ preventScroll: true });
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
        // Noted before the menu closes, which drops the focus.
        const where = focusOf(form);
        // What it was, so the change can be taken back.
        const was = form.querySelector('[aria-current="true"]');
        const action = form.action;
        form.closest("details")?.removeAttribute("open");
        if (btn && was === btn) return;
        try {
          await post(action, fields);
          await refresh(where);
          const field = { status: "Status", priority: "Priority", project: "Project", cycle: "Cycle" }[btn?.name];
          const label = btn?.querySelector("span")?.textContent.trim();
          const undo = was && btn?.name ? async () => {
            try {
              await post(action, { [btn.name]: was.value });
              await refresh(where);
              say(`${field || "Change"} put back.`);
            } catch (err) {
              say(err.message, true);
            }
          } : null;
          say(label ? `${field ? field + " set" : "Set"} to ${label}.` : "Saved.", false, undo);
        } catch (err) {
          say(err.message, true);
        }
      });
    }
  };

  // One menu open at a time; a click elsewhere or Escape closes it. A menu
  // that held the focus hands it back to its button, so it isn't left on
  // something hidden.
  const closeMenus = (except) => {
    for (const d of $$("details.qmenu[open]")) {
      if (d === except) continue;
      const had = d.contains(document.activeElement);
      d.open = false;
      if (had) d.querySelector("summary")?.focus();
    }
  };
  document.addEventListener("toggle", (e) => {
    const d = e.target;
    if (d.matches?.("details.qmenu") && d.open) {
      closeMenus(d);
      (d.querySelector('[aria-current="true"]') || d.querySelector("button"))?.focus();
    }
    // The shortcut list, closed by its own button, goes away entirely.
    if (d.id === "shortcuts" && !d.open && d.classList.contains("floating")) showHelp(false);
  }, true);
  document.addEventListener("click", (e) => {
    if (!e.target.closest("details.qmenu")) closeMenus();
  });

  // Arrow keys move through an open menu, and a digit picks the option
  // with that number.
  document.addEventListener("keydown", (e) => {
    const menu = e.target.closest?.("details.qmenu[open]");
    if (!menu) return;
    if (/^[0-9]$/.test(e.key) && !e.metaKey && !e.ctrlKey && !e.altKey) {
      const pick = $$("button", menu).find((b) => b.querySelector(".qkey")?.textContent === e.key);
      if (pick) { pick.click(); e.preventDefault(); }
      return;
    }
    if (!["ArrowDown", "ArrowUp"].includes(e.key)) return;
    const items = $$("button", menu);
    const i = items.indexOf(document.activeElement);
    const next = items[(i + (e.key === "ArrowDown" ? 1 : -1) + items.length) % items.length];
    next?.focus();
    e.preventDefault();
  });

  // --- The board: drag cards between and within columns ---

  const board = () => {
    const root = $(".board");
    if (!root) return;
    // On a narrow screen one column shows at a time: open on the work in
    // flight rather than the backlog, once, not after every change.
    if (!board.placed) {
      board.placed = true;
      const start = $('.bcol[data-status="in_progress"]', root);
      if (start && root.scrollWidth > root.clientWidth) root.scrollLeft = start.offsetLeft - root.offsetLeft;
    }
    if (!matchMedia("(pointer: fine)").matches) return;
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
          await refresh({ focusKey: card.dataset.issue, menuAt: -1 });
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
  const rows = () => $$(".irow .ititle, .bcard .ititle, .projname");
  const go = (href) => { if (href) location.href = href; };
  // A view of this repository's issues, from wherever in them we are. Across
  // every repository there's only the list.
  const viewLink = (name) => {
    const at = location.pathname.indexOf("/-/issues");
    if (at < 0 || (at === 0 && name !== "")) return null;
    return location.pathname.slice(0, at) + "/-/issues" + name;
  };
  let pendingG = false;

  // The list stays out of the way until asked for with ?; a line at the
  // page's foot says so. Keys that do nothing on this page aren't listed.
  const shortcuts = () => {
    const hint = $("#keyhint");
    if (hint) hint.hidden = false;
    for (const dt of $$("#shortcuts dt[data-needs]")) {
      const there = !!$(dt.dataset.needs);
      dt.hidden = !there;
      if (dt.nextElementSibling) dt.nextElementSibling.hidden = !there;
    }
  };
  const showHelp = (show) => {
    const panel = $("#shortcuts");
    if (!panel) return;
    panel.hidden = !show;
    panel.classList.toggle("floating", show);
    panel.open = show;
  };

  // Changing a filter shows its result; the Show button is for no script.
  const filters = () => {
    for (const sel of $$(".issuefilter select")) {
      sel.addEventListener("change", () => sel.form.requestSubmit());
    }
  };

  document.addEventListener("keydown", (e) => {
    // Cmd or Ctrl with Enter sends the form being typed in.
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && typing(e.target) && e.target.form) {
      e.target.form.requestSubmit();
      e.preventDefault();
      return;
    }
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "Escape") {
      if ($("details.qmenu[open]")) { closeMenus(); e.preventDefault(); return; }
      if ($("#shortcuts.floating[open]")) { showHelp(false); e.preventDefault(); return; }
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
        // The list floats over the page.
        const panel = $("#shortcuts");
        if (!panel) return;
        showHelp(panel.hidden || !panel.open);
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
        // From the chosen row, even when its menu button has the focus.
        const here = document.activeElement?.closest("[data-issue]")?.querySelector(".ititle") || document.activeElement;
        const i = list.indexOf(here);
        const next = i < 0 ? (e.key === "j" ? 0 : list.length - 1) : Math.min(list.length - 1, Math.max(0, i + (e.key === "j" ? 1 : -1)));
        list[next].focus();
        list[next].scrollIntoView({ block: "nearest" });
        break;
      }
      case "s":
      case "p": {
        const which = e.key === "s" ? ".istat" : ".ipri";
        // The chosen row's menu, or on an issue's page, its own.
        const menu = focused?.querySelector(`details.qmenu > summary${which}`)?.parentElement
          || $(`.iprops details.qmenu > summary${which}`)?.parentElement;
        if (menu) { menu.open = true; break; }
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

  // Styles that only make sense with the script running hang off this.
  document.documentElement.classList.add("scripted");

  const setup = () => {
    menus();
    board();
    shortcuts();
    filters();
  };
  setup();
})();
