(function() {
  const iframes = document.querySelectorAll('iframe');
  let best = null, maxArea = 0;
  for (const f of iframes) {
    if (!f.src || !/^https?:\/\//i.test(f.src)) continue;
    const style = getComputedStyle(f);
    if (style.display === 'none' || style.visibility !== 'visible' || Number(style.opacity) === 0) continue;
    const r = f.getBoundingClientRect();
    const a = r.width * r.height;
    if (a > maxArea && r.width > 100 && r.height > 100) { maxArea = a; best = f; }
  }
  if (best) {
    best.scrollIntoView({block: 'center', behavior: 'instant'});
    return best.src;
  }
  return null;
})()
