export function* inner() {
  yield 1;
  yield 2;
}
export function* outer() {
  yield* inner();
  yield 3;
}
//# sourceMappingURL=./index.js.map
