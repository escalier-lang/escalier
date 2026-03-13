export function* count() {
  yield 1;
  yield 2;
  yield 3;
}
export function* countWithDone() {
  yield 1;
  yield 2;
  return "done";
}
export function* mixed() {
  yield 1;
  yield "hello";
}
//# sourceMappingURL=./index.js.map
