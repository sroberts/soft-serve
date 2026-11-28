// Copy buttons: each button[data-copy] copies the text of the element its
// value names. The buttons ship hidden and appear only where the clipboard
// can be written, so without this script, or over plain HTTP, the text is
// still there to select by hand.
(() => {
  "use strict";
  if (!navigator.clipboard || !window.isSecureContext) return;
  for (const btn of document.querySelectorAll("button[data-copy]")) {
    const from = document.getElementById(btn.dataset.copy);
    if (!from) continue;
    const label = btn.getAttribute("aria-label");
    let timer;
    btn.hidden = false;
    btn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(from.textContent.trim());
      } catch {
        return;
      }
      btn.classList.add("copied");
      btn.setAttribute("aria-label", "Copied");
      clearTimeout(timer);
      timer = setTimeout(() => {
        btn.classList.remove("copied");
        btn.setAttribute("aria-label", label);
      }, 1500);
    });
  }
})();
