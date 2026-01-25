export function makeArray(param0, param1) {
  if (typeof param0 === "number" && typeof param1 === "number") {
    const value = param0;
    const count = param1;
  } else if (typeof param0 === "number") {
    const value = param0;
  } else throw new TypeError("No overload matches the provided arguments for function 'makeArray'");
}
export const arr1 = makeArray(5);
export const arr2 = makeArray(5, 3);
export function format(param0) {
  if (typeof param0 === "number") {
    const value = param0;
  } else if (typeof param0 === "string") {
    const value = param0;
  } else throw new TypeError("No overload matches the provided arguments for function 'format'");
}
export function identity(param0) {
  if (true) {
    const value = param0;
  } else if (typeof param0 === "string") {
    const value = param0;
  } else throw new TypeError("No overload matches the provided arguments for function 'identity'");
}
export const genResult = identity("world");
export function process(param0) {
  if (typeof param0 === "number") {
    const value = param0;
  } else if (typeof param0 === "string") {
    const value = param0;
  } else if (typeof param0 === "boolean") {
    const value = param0;
  } else throw new TypeError("No overload matches the provided arguments for function 'process'");
}
export const r1 = process(123);
export const r2 = process("test");
export const r3 = process(true);
export const result1 = format(42);
export const result2 = format("hello");
//# sourceMappingURL=./index.js.map
