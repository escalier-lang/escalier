export function go() {
  const p = {x: 1};
  const q = {x: 2};
  const t = [q];
  const i = 0;
  t[i] = p;
  return p.x + t[i].x;
}
//# sourceMappingURL=./index.js.map
