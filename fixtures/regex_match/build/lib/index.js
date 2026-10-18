export function classify(temp1) {
  const s = temp1;
  let temp2;
  let temp3;
  temp3 = s;
  if (typeof temp3 === "string" && /^\d+$/.test(temp3)) {
    const d = temp3;
    temp2 = "digits";
  } else if (typeof temp3 === "string" && /^[a-z]+$/i.test(temp3)) {
    const w = temp3;
    temp2 = "word";
  } else {
    temp2 = "other";
  }
  return temp2;
}
export function firstDigits(temp4) {
  const p = temp4;
  let temp5;
  let temp6;
  temp6 = p;
  if (temp6.length == 2 && typeof temp6[0] === "string" && /^\d+$/.test(temp6[0])) {
    const [d, n] = temp6;
    temp5 = n;
  } else {
    temp5 = 0;
  }
  return temp5;
}
export function kind(temp7) {
  const v = temp7;
  let temp8;
  let temp9;
  temp9 = v;
  if (typeof temp9 === "string" && /^[a-z]+$/.test(temp9) || typeof temp9 === "string" && /^\d+$/.test(temp9)) {
    const i = temp9;
    temp8 = "id";
  } else if (typeof temp9 === "number" || typeof temp9 === "boolean") {
    const n = temp9;
    temp8 = "scalar";
  } else {
    temp8 = "other";
  }
  return temp8;
}
//# sourceMappingURL=./index.js.map
