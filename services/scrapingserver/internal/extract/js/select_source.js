function(skip) {
  // Finds a page's list of alternative sources (servers, mirrors, streams) and says where the
  // skip-th usable one is, so castor can click it. The rules are written up in SOURCES.md beside
  // this file's package, and numbered the same way here.
  const HEADING = /\b(available\s+)?(sources?|servers?|streams?|mirrors?|links?)\b/i;
  // R1: a panel that merely sits beside a player (its controls, chat, schedule) is not a list of sources.
  const NOT_SOURCES = /\b(controls?|settings?|chat|comments?|share|related|schedules?|more|menu|navigation)\b/i;
  // R3: a promotion, a download or a sign-up is not a stream.
  const NOT_A_STREAM = /\b(ads?|advert\w*|sponsor\w*|promo\w*|bet(ting)?|casino|vpn|telegram|discord|download|install|apps?|register|sign\s?up|log\s?in|premium|vip|donate|subscribe)\b/i;

  const visible = e => {
    const s = e.ownerDocument.defaultView.getComputedStyle(e), r = e.getBoundingClientRect();
    return s.display !== 'none' && s.visibility === 'visible' && Number(s.opacity) !== 0 && r.width >= 24 && r.height >= 14;
  };
  const clean = t => (t || '').replace(/\s+/g, ' ').trim();

  // R1: a panel is a group whose own heading names sources, found by the legend, heading or label it carries.
  const headingOf = group => {
    const own = group.querySelector(':scope > legend, :scope > h1, :scope > h2, :scope > h3, :scope > h4, :scope > h5, :scope > h6, :scope > [role="heading"]');
    return clean(own ? own.textContent : group.getAttribute('aria-label'));
  };
  const panels = [...document.querySelectorAll('fieldset, section, div[role="group"], [role="tablist"], [aria-label]')]
    .filter(g => {
      const h = headingOf(g);
      // R1b: a site's navigation is not a list of sources, whatever it is titled.
      return h && h.length <= 48 && HEADING.test(h) && !NOT_SOURCES.test(h) && !g.closest('nav, header, footer');
    });

  const CLICKABLE = 'button, a[href], [role="button"], [role="tab"], [role="radio"]';
  const labelOf = e => clean(e.getAttribute('aria-label') || e.textContent);

  const usable = [];
  for (const panel of panels) {
    // R2: only the outermost clickable of each item counts; a button inside a button is one source.
    const items = [...panel.querySelectorAll(CLICKABLE)].filter(e => !e.parentElement.closest(CLICKABLE) || !panel.contains(e.parentElement.closest(CLICKABLE)));
    for (const e of items) {
      if (!visible(e) || e.disabled || e.getAttribute('aria-disabled') === 'true') continue;
      const label = labelOf(e);
      // R3: left out by what it says...
      if (!label || NOT_A_STREAM.test(label)) continue;
      // ...and by where it goes: a link that opens another tab cannot be cast from this one.
      if (e.tagName === 'A' && e.getAttribute('target') === '_blank') continue;
      // R3b: and a link to another page is a menu entry. A source that is a link stays on this page (?source=2, #2).
      if (e.tagName === 'A') {
        const to = new URL(e.href, location.href);
        if (to.origin !== location.origin || to.pathname !== location.pathname) continue;
      }
      usable.push({e, label, panel: headingOf(panel)});
    }
    // R4: the first panel that has any usable source is the list; later ones are not consulted.
    if (usable.length > 0) break;
  }

  // R5: the page's own order is its ranking, so the first is tried first and skip says how many were tried.
  const pick = usable[skip];
  if (!pick) return {total: usable.length, picked: false};
  pick.e.scrollIntoView({block: 'center', behavior: 'instant'});
  const r = pick.e.getBoundingClientRect();
  return {
    total: usable.length,
    picked: true,
    label: pick.label,
    panel: pick.panel,
    // R6: a source that already shows as pressed is the page's own choice; clicking it again would only restart it.
    selected: pick.e.getAttribute('aria-pressed') === 'true' || /--selected\b/.test(pick.e.className),
    x: r.left + r.width / 2,
    y: r.top + r.height / 2,
  };
}
