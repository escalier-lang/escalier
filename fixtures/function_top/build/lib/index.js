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
export function hasCallback(temp4) {
  const u = temp4;
  let temp5;
  let temp6;
  temp6 = u;
  if (typeof temp6 === "function" || temp6 === undefined) {
    const c = temp6;
    temp5 = true;
  } else if (typeof temp6 === "number") {
    const n = temp6;
    temp5 = false;
  }
  return temp5;
}
export function kind(temp7) {
  const u = temp7;
  let temp8;
  let temp9;
  temp9 = u;
  if (typeof temp9 === "function") {
    const f = temp9;
    temp8 = "function";
  } else if (typeof temp9 === "number") {
    const n = temp9;
    temp8 = "number";
  }
  return temp8;
}
//# sourceMappingURL=./index.js.map
