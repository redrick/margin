const overflowOrder = ["export", "help", "github", "layout", "notes", "filter", "search", "tests", "display", "grouping"];

export function reviewToolbarOverflow(toolbar) {
  const more = toolbar.querySelector("#toolbar-more");
  const panel = toolbar.querySelector("#toolbar-overflow");
  const actions = toolbar.querySelector("#toolbar-actions");
  const search = toolbar.querySelector("#search");
  const homes = new Map();
  let scheduled = false;

  function setOpen(open, focusMore = false) {
    panel.inert = !open;
    more.setAttribute("aria-expanded", String(open));
    if (focusMore) more.focus({ preventScroll: true });
  }

  more.onclick = () => {
    setOpen(panel.inert);
    if (!panel.inert) panel.querySelector("button:not(.hidden), select, input")?.focus();
  };
  toolbar.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || panel.inert) return;
    e.preventDefault();
    e.stopPropagation();
    setOpen(false, true);
  });
  document.addEventListener("pointerdown", (e) => {
    if (!panel.inert && !panel.contains(e.target) && !more.contains(e.target)) setOpen(false);
  });
  document.addEventListener("focusin", (e) => {
    if (!panel.inert && !panel.contains(e.target) && e.target !== more) setOpen(false);
  });

  function layout() {
    scheduled = false;
    if (!toolbar.clientWidth) return;
    const homeOf = (item) => homes.get(item) ?? item;
    const items = [...toolbar.querySelectorAll("[data-overflow]")].sort((a, b) => {
      if (a === b) return 0;
      return homeOf(a).compareDocumentPosition(homeOf(b)) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1;
    });
    const style = getComputedStyle(toolbar);
    const gap = parseFloat(style.columnGap);
    const available = toolbar.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
    const visible = items.filter((item) => !item.classList.contains("hidden"));
    const widths = new Map(visible.map((item) => {
      const size = getComputedStyle(item);
      return [item, item.getBoundingClientRect().width + parseFloat(size.marginLeft) + parseFloat(size.marginRight) + gap];
    }));
    let used = [...widths.values()].reduce((sum, width) => sum + width, 0);
    const moved = new Set();
    if (used - gap > available) {
      // Each remaining control accounts for the gap before More.
      used += more.getBoundingClientRect().width;
      for (const key of overflowOrder) {
        if (used <= available) break;
        for (const item of visible) {
          if (item.dataset.overflow !== key) continue;
          used -= widths.get(item);
          moved.add(item);
        }
      }
    }

    const focused = document.activeElement;
    more.classList.toggle("available", moved.size > 0);
    if (!moved.size && !panel.inert) setOpen(false);
    for (const [item, home] of homes) {
      if (moved.has(item)) continue;
      home.replaceWith(item);
      homes.delete(item);
    }
    let next = panel.firstElementChild;
    for (const item of items) {
      if (!moved.has(item)) continue;
      if (!homes.has(item)) {
        const home = document.createComment("toolbar control");
        item.before(home);
        homes.set(item, home);
      }
      if (item !== next) panel.insertBefore(item, next);
      next = item.nextElementSibling;
    }

    if (focused === more && !moved.size) {
      const first = visible[0];
      (first?.matches("button, select, input") ? first : first?.querySelector("button, select, input"))?.focus({ preventScroll: true });
    } else if (focused !== document.activeElement && toolbar.contains(focused)) {
      if (panel.contains(focused) && panel.inert) {
        if (focused === search) setOpen(true);
        else return more.focus({ preventScroll: true });
      }
      focused.focus({ preventScroll: true });
    }
  }

  function schedule() {
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(layout);
  }

  new ResizeObserver(schedule).observe(toolbar);

  return {
    schedule,
    clearActions() {
      for (const [item, home] of homes) {
        if (home.parentNode !== actions) continue;
        if (item.contains(document.activeElement)) more.focus({ preventScroll: true });
        item.remove();
        homes.delete(item);
      }
      actions.replaceChildren();
    },
    closeForAction(item) {
      if (panel.contains(item)) setOpen(false, true);
    },
    focusSearch() {
      if (panel.contains(search)) setOpen(true);
      search.focus();
    },
    finishSearch() {
      if (panel.contains(search)) setOpen(false, true);
      else search.blur();
    },
  };
}
