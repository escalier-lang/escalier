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
export function drive() {
  const it = count();
  return it.next();
}
export function* resumable() {
  const sent = yield 1;
  return sent;
}
export function driveResumable() {
  const it = resumable();
  return it.next("go");
}
export function driveWithValue() {
  const it = count();
  return it.next("resume");
}
export async function* fetchItems() {
  yield 1;
  yield 2;
  yield 3;
}
export function* genCount() {
  yield 10;
  yield 20;
}
export const genExpr = function* () {
  yield "a";
};
export async function* genFetch() {
  yield 1;
}
export function* genNoYield() {
  return 1;
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
export function* relayResumable() {
  yield* resumable();
}
export function sumOuter() {
  let total = 0;
  for (const temp1 of outer()) {
    const n = temp1;
    total = total + n;
  }
  return total;
}
//# sourceMappingURL=./internal.js.map
