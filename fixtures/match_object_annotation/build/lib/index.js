export function plain(temp1) {
  const u = temp1;
  let temp2;
  let temp3;
  temp3 = u;
  if (temp3.kind === "b") {
    const x = temp3;
    temp2 = x.b.length;
  } else if (temp3.kind === "a") {
    const y = temp3;
    temp2 = y.a;
  }
  return temp2;
}
export const fromA = plain({kind: "a", a: 1});
export const fromB = plain({kind: "b", b: "hi"});
//# sourceMappingURL=./index.js.map
