const color = new Color.Hex("#FF0000");
let temp1;
let temp2;
temp2 = color;
if (temp2 instanceof Color.RGB) {
  const [temp4, temp5, temp6] = InvokeCustomMatcherOrThrow(Color.RGB, temp2, undefined);
  const r = temp4;
  const g = temp5;
  const b = temp6;
  temp1 = r + g + b;
} else if (temp2 instanceof Color.Hex) {
  const [temp3] = InvokeCustomMatcherOrThrow(Color.Hex, temp2, undefined);
  const code = temp3;
  temp1 = code;
}
const result = temp1;
console.log("hello, world");
//# sourceMappingURL=./index.js.map
