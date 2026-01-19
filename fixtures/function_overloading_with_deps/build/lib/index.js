export function process(param0) {
  if (true) {
    const config = param0;
    return "Number: " + config.value.toString();
  } else if (true) {
    const config = param0;
    return "String: " + config.text;
  } else if (true) {
    const config = param0;
    let temp1;
    if (config.flag) {
      return "Boolean: true";
    } else {
      return "Boolean: false";
    }
    temp1;
  } else throw TypeError("No overload matches the provided arguments for function 'process'");
}
export const numResult = process({value: 42});
export const strResult = process({text: "hello"});
export const boolResult = process({flag: true});
//# sourceMappingURL=./index.js.map
