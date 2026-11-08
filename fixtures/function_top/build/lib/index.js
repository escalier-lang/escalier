export class Box {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
}
export function describe(temp2) {
  const f = temp2;
  return "callable";
}
export const fs = [function (temp3) {
  const x = temp3;
  return x;
}, function () {
}, Box];
export function kind(temp4) {
  const u = temp4;
  let temp5;
  let temp6;
  temp6 = u;
  if (typeof temp6 === "function") {
    const f = temp6;
    temp5 = "function";
  } else if (typeof temp6 === "number") {
    const n = temp6;
    temp5 = "number";
  }
  return temp5;
}
//# sourceMappingURL=./index.js.map
