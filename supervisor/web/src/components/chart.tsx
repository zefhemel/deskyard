// A hand-rolled area chart, like the mockup's: values 0..max over a fixed
// window, newest on the right.
const W = 300;
const H = 70;

export function Chart({ values, max, slots }: { values: number[]; max: number; slots: number }) {
  const step = W / Math.max(1, slots - 1);
  // Right-align: the newest value sits at the right edge even before the window fills.
  const offset = (slots - values.length) * step;
  const pts = values.map((v, i) => {
    const y = H - (Math.max(0, Math.min(max, v)) / (max || 1)) * H;
    return `${(offset + i * step).toFixed(1)},${y.toFixed(1)}`;
  });
  const line = pts.join(" ");
  const first = values.length ? (offset).toFixed(1) : "0";
  return (
    <svg class="block h-[70px] w-full" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true">
      <line x1="0" y1={H / 2} x2={W} y2={H / 2} stroke="#d3d9e0" stroke-dasharray="3 4" vector-effect="non-scaling-stroke" />
      {values.length > 1 && (
        <>
          <polygon points={`${first},${H} ${line} ${W},${H}`} fill="#172231" opacity=".12" />
          <polyline points={line} fill="none" stroke="#172231" stroke-width="1.8" vector-effect="non-scaling-stroke" />
        </>
      )}
    </svg>
  );
}
