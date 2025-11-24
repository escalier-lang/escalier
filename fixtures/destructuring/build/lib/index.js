export const obj = {a: true, b: false, c: 0, e: "foo", f: "bar", g: "baz"};
export const {a, b = 5, c: d, e: f = "hello", ...rest} = obj;
export const array = [5, 10, "foo", "bar"];
export const [fst, snd = true, ...other] = array;
export const p = {x: 5, y: 10};
export const {x, y, z} = p;
export const {x: x1} = p;
//# sourceMappingURL=./index.js.map
