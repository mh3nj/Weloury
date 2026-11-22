import { useEffect, useState } from "react";

/**
 * Live viewport width. The sidebar picks its display mode from this rather
 * than a Tailwind breakpoint because the three modes (icons only, labels
 * only, both) switch on custom thresholds that a utility class can't
 * express cleanly, and a resized desktop window needs to re-evaluate them.
 */
export function useWindowWidth(): number {
  const [width, setWidth] = useState(() =>
    typeof window === "undefined" ? 1320 : window.innerWidth
  );

  useEffect(() => {
    function onResize() {
      setWidth(window.innerWidth);
    }
    window.addEventListener("resize", onResize);
    onResize();
    return () => window.removeEventListener("resize", onResize);
  }, []);

  return width;
}
