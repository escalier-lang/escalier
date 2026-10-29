export function forEachItem(temp1) {
  const f = temp1;
  f();
}
export const handlers = [];
export function onClick(temp2) {
  const handler = temp2;
  handlers.push(handler);
}
export function runWith(temp3, temp4) {
  const f = temp3;
  const p = temp4;
  return f(p.x);
}
export function setup() {
  const count = {n: 0};
  onClick(function () {
    count.n = count.n + 1;
  });
  forEachItem(function () {
    count.n = count.n + 1;
  });
  const p = {x: 1};
  return runWith(function (temp5) {
    const x = temp5;
    return x + 1;
  }, p);
}
//# sourceMappingURL=./index.js.map
