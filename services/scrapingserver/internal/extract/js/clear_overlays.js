(function() {
  const MARK = 'data-castor-overlay';
  if (!document.getElementById('castor-overlay-style')) {
    const style = document.createElement('style');
    style.id = 'castor-overlay-style';
    // A stylesheet rule outlives the page resetting the element's inline style.
    style.textContent = '[' + MARK + ']{pointer-events:none!important;display:none!important}';
    (document.head || document.documentElement).appendChild(style);
  }
  const visible = e => {
    const s = e.ownerDocument.defaultView.getComputedStyle(e), r = e.getBoundingClientRect();
    return s.display !== 'none' && s.visibility === 'visible' && Number(s.opacity) !== 0 && r.width > 32 && r.height > 32;
  };
  const area = r => Math.max(0, r.width) * Math.max(0, r.height);
  const overlap = (a, b) => area({
    width: Math.min(a.right, b.right) - Math.max(a.left, b.left),
    height: Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top),
  });
  // What the viewer came to watch: the largest video or player frame.
  const targetOf = doc => {
    let best = null, bestArea = 0;
    for (const e of doc.querySelectorAll('video, iframe')) {
      if (!visible(e)) continue;
      const a = area(e.getBoundingClientRect());
      if (a > bestArea) { best = e; bestArea = a; }
    }
    return best;
  };
  // The player's own click layer (as in JW Player) is a sibling inside its wrapper; traps hang off the page.
  const withPlayer = (e, target) => {
    let a = e.parentElement;
    while (a && !a.contains(target)) a = a.parentElement;
    return !!a && a !== e.ownerDocument.body && a !== e.ownerDocument.documentElement;
  };
  // A click trap is empty: it draws nothing and says nothing, it only swallows the click (usually to open an ad).
  const trap = (e, target, tr) => {
    if (e === target || e.contains(target) || e.hasAttribute(MARK) || withPlayer(e, target)) return false;
    const view = e.ownerDocument.defaultView;
    const s = view.getComputedStyle(e);
    if (s.pointerEvents === 'none' || !['fixed', 'absolute', 'sticky'].includes(s.position)) return false;
    if (overlap(e.getBoundingClientRect(), tr) < area(tr) / 2) return false;
    if (e.textContent.trim() !== '') return false;
    return !e.querySelector('img, video, canvas, audio, input, textarea, select, button, svg');
  };
  let cleared = 0;
  const clear = (doc, depth) => {
    const target = targetOf(doc);
    if (target) {
      const tr = target.getBoundingClientRect();
      const x = Math.min(Math.max(tr.left + tr.width / 2, 0), doc.documentElement.clientWidth - 1);
      const y = Math.min(Math.max(tr.top + tr.height / 2, 0), doc.documentElement.clientHeight - 1);
      // The stack is topmost first; everything above the target is in the way.
      for (const e of doc.elementsFromPoint(x, y)) {
        if (e === target || e.contains(target)) break;
        if (trap(e, target, tr)) {
          e.setAttribute(MARK, '');
          cleared++;
        }
      }
    }
    if (depth >= 5) return;
    for (const frame of doc.querySelectorAll('iframe')) {
      try {
        if (frame.contentDocument && visible(frame)) clear(frame.contentDocument, depth + 1);
      } catch (_) { /* Cross-origin frames are cleared once navigated to. */ }
    }
  };
  clear(document, 0);
  return cleared;
})()
