export const obj1 = {a: 5, b: "hello", c: true};
export const a = obj1.a;
export const b = obj1["b"];
export const c = obj1["c"];
export const d = obj2.d;
export const f = obj2.e?.f;
export const g = obj3.bar;
export const key = "c";
export const p = {x: 0, y: 0};
export function main() {
  p.x = 5;
  p.y = 10;
  obj3.bar = "hello";
}
export const obj4 = {a, b, c};
//# sourceMappingURL=./index.js.map
