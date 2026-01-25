export function process(param0) {
  if (true) {
    const config = param0;
    return "Number: " + config.value.toString();
  } else if (true) {
    const config = param0;
    return "String: " + config.text;
  } else if (true) {
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
