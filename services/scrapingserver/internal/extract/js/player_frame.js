function(scroll) {
  // Lists the iframes of the page that could be its player, with where a click in each would land.
  // Which of them are chat or social widgets is decided by the caller (chatframes.go), which also picks the largest.
  // With scroll, each frame is brought into view first so that x and y can be clicked; without it the page is left as it is.
  const frames = [];
  for (const f of document.querySelectorAll('iframe')) {
    if (!/^https?:\/\//i.test(f.src)) continue;
    const style = getComputedStyle(f);
    if (style.display === 'none' || style.visibility !== 'visible' || Number(style.opacity) === 0) continue;
    let r = f.getBoundingClientRect();
    if (r.width < 200 || r.height < 120) continue;
    if (scroll) {
      f.scrollIntoView({block: 'center', behavior: 'instant'});
      r = f.getBoundingClientRect();
    }
    // A frame taller or wider than the window is clicked where it can be seen.
    const left = Math.max(r.left, 0), right = Math.min(r.right, window.innerWidth);
    const top = Math.max(r.top, 0), bottom = Math.min(r.bottom, window.innerHeight);
    if (scroll && (right <= left || bottom <= top)) continue;
    frames.push({
      src: f.src,
      width: r.width,
      height: r.height,
      // A player is embedded with the permissions it needs to play; an ad or a widget usually is not.
      hint: /\b(autoplay|encrypted-media|fullscreen)\b/i.test(f.allow || '') || f.hasAttribute('allowfullscreen'),
      x: (left + right) / 2,
      y: (top + bottom) / 2,
    });
  }
  return frames;
}
