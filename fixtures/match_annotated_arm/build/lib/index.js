let temp1;
let temp2;
temp2 = numOrStr;
if (typeof temp2 === "number") {
  const n = temp2;
  temp1 = n + 1;
} else if (typeof temp2 === "string") {
  const s = temp2;
  temp1 = s.length;
}
export const annotatedArms = temp1;
let temp3;
let temp4;
temp4 = numOrStr;
if (typeof temp4 === "number") {
  const n = temp4;
  temp3 = n.toString();
} else {
  const other = temp4;
  temp3 = other;
}
export const annotatedThenCatchAll = temp3;
let temp5;
let temp6;
temp6 = pair;
if (temp6.length == 2 && typeof temp6[0] === "number" && typeof temp6[1] === "string") {
  const [a, b] = temp6;
  temp5 = b.length + a;
}
export const nestedLeafAnnotations = temp5;
let temp7;
let temp8;
temp8 = litsOrStr;
if (typeof temp8 === "number") {
  const n = temp8;
  temp7 = n;
} else if (typeof temp8 === "string") {
  const s = temp8;
  temp7 = s.length;
}
export const widerThanAMember = temp7;
//# sourceMappingURL=./index.js.map
