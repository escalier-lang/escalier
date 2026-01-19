export function dup(param0) {
  if (typeof param0 === "number") {
    const value = param0;
    return 2 * value;
  } else if (typeof param0 === "string") {
    const value = param0;
    return value + value;
  } else throw TypeError("No overload matches the provided arguments for function 'dup'");
}
export const num = dup(5);
export const str = dup("hello");
export function format(param0) {
  if (typeof param0 === "number") {
    const value = param0;
    return "Number: " + value.toString();
  } else if (typeof param0 === "string") {
    const value = param0;
    return "String: " + value;
  } else if (typeof param0 === "boolean") {
    const value = param0;
    let temp1;
    if (value) {
      return "Boolean: true";
    } else {
      return "Boolean: false";
    }
    temp1;
  } else throw TypeError("No overload matches the provided arguments for function 'format'");
}
export const f1 = format(42);
export const f2 = format("test");
export const f3 = format(true);
//# sourceMappingURL=./index.js.map
