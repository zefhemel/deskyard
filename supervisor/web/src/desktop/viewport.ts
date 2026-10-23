/** iOS Home Screen apps can omit their top inset from both JS heights,
 * while 100lvh still describes the full screen. Restore only that measured gap. */
export function visibleViewport(visual: { height: number; offsetTop: number }, layoutHeight: number, standaloneHeight = layoutHeight) {
  return {
    height: visual.height + Math.max(0, standaloneHeight - layoutHeight),
    top: visual.offsetTop,
    keyboard: layoutHeight - visual.height - visual.offsetTop > 100,
  };
}
