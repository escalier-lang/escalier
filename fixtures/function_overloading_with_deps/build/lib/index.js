export function process(param0) {
  if (arguments.length === 1 && param0 !== null && typeof param0 === "object" && "value" in param0 && typeof param0.value === "number") {
    const config = param0;
    return "Number: " + config.value.toString();
  } else if (arguments.length === 1 && param0 !== null && typeof param0 === "object" && "text" in param0 && typeof param0.text === "string") {
    const config = param0;
    return "String: " + config.text;
  } else if (arguments.length === 1 && param0 !== null && typeof param0 === "object" && "flag" in param0 && typeof param0.flag === "boolean") {
    const config = param0;
    if (config.flag) {
      return "Boolean: true";
    } else {
      return "Boolean: false";
    }
  } else throw new TypeError("No overload matches the provided arguments for function 'process'");
}
export const boolResult = process({flag: true});
export const numResult = process({value: 42});
export const strResult = process({text: "hello"});
//# sourceMappingURL=./index.js.map
