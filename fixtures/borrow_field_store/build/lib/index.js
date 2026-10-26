export function go() {
  const p = {x: 1};
  const q = {x: 2};
  const h = {slot: q};
  h.slot = p;
  return p.x + h.slot.x;
}
//# sourceMappingURL=./index.js.map
