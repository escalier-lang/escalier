export function charCount(temp1) {
  const s = temp1;
  let count = 0;
  for (const temp2 of s) {
    const ch = temp2;
    count = count + 1;
  }
  return count;
}
export function sumArray(temp3) {
  const items = temp3;
  let total = 0;
  for (const temp4 of items) {
    const item = temp4;
    total = total + item;
  }
  return total;
}
export function sumPairs(temp5) {
  const pairs = temp5;
  let total = 0;
  for (const temp6 of pairs) {
    const [a, b] = temp6;
    total = total + a + b;
  }
  return total;
}
//# sourceMappingURL=./index.js.map
