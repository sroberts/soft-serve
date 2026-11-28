// Copy buttons: each button[data-copy] copies the text of the element just
// before it. The buttons ship hidden and appear only where the clipboard
// can be written, so without this script, or over plain HTTP, the text is
// still there to select by hand.
//
// The target is the button's own neighbour, not an element looked up by
// id: a README can give its headings ids, and a lookup could find one of
// those instead.
(() => {
  "use strict";
  if (!navigator.clipboard || !window.isSecureContext) return;
  let live;
  const announce = (text) => {
    if (!live) {
      live = document.createElement("p");
      live.className = "visually-hidden";
      live.setAttribute("role", "status");
      document.body.append(live);
    }
    live.textContent = "";
    live.textContent = text;
  };
  for (const btn of document.querySelectorAll("button[data-copy]")) {
    const from = btn.previousElementSibling;
    if (!from || from.id !== btn.dataset.copy) continue;
    let timer;
    btn.title = btn.getAttribute("aria-label");
    btn.hidden = false;
    btn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(from.textContent.trim());
      } catch {
        // Select it instead, so it can be copied by hand.
        getSelection().selectAllChildren(from);
        announce("Couldn't copy. The text is selected; copy it by hand.");
        return;
      }
      btn.classList.add("copied");
      announce("Copied.");
      clearTimeout(timer);
      timer = setTimeout(() => btn.classList.remove("copied"), 1500);
    });
  }
})();
