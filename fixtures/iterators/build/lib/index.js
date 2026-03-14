export function charCount(temp1) {
  const s = temp1;
  let count = 0;
  for (const temp2 of s) {
    const ch = temp2;
    count = count + 1;
  }
  return count;
}
export function spreadArray(temp3) {
  const items = temp3;
  return [...items];
}
export function spreadString(temp4) {
  const s = temp4;
  return [...s];
}
export function sumArray(temp5) {
  const items = temp5;
  let total = 0;
  for (const temp6 of items) {
    const item = temp6;
    total = total + item;
  }
  return total;
}
export function sumPairs(temp7) {
  const pairs = temp7;
  let total = 0;
  for (const temp8 of pairs) {
    const [a, b] = temp8;
    total = total + a + b;
  }
  return total;
}
//# sourceMappingURL=./index.js.map
