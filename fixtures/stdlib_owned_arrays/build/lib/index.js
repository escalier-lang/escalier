export function concatAll(temp1, temp2) {
  const xs = temp1;
  const ys = temp2;
  return xs.concat(ys);
}
export function largest(temp3, temp4) {
  const a = temp3;
  const b = temp4;
  return Math.max(a, b, 0);
}
export function lower(temp5) {
  const s = temp5;
  return s.toLocaleLowerCase(["en-US", "fr"]);
}
export function ofThenPush() {
  const ys = Array.of(1, 2);
  return ys.push(3);
}
export function pushAll(temp6) {
  const xs = temp6;
  xs.push(1, 2, 3);
  xs.unshift(0);
  return xs;
}
export async function settle(temp7) {
  const ps = temp7;
  const results = await Promise.all(ps);
  results.reverse();
  return results.length;
}
//# sourceMappingURL=./index.js.map
