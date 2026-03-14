export function* count() {
  yield 1;
  yield 2;
  yield 3;
}
export function countArray() {
  return [...count()];
}
export function* countWithDone() {
  yield 1;
  yield 2;
  return "done";
}
export async function* fetchItems() {
  yield 1;
  yield 2;
  yield 3;
}
export function* inner() {
  yield 1;
  yield 2;
}
export function* mixed() {
  yield 1;
  yield "hello";
}
export function* outer() {
  yield* inner();
  yield 3;
}
export function outerArray() {
  return [...outer()];
}
export function sumOuter() {
  let total = 0;
  for (const temp1 of outer()) {
    const n = temp1;
    total = total + n;
  }
  return total;
}
//# sourceMappingURL=./index.js.map
