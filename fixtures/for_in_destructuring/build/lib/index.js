export function sumPairs(temp1) {
  const pairs = temp1;
  let total = 0;
  for (const temp2 of pairs) {
    const [a, b] = temp2;
    total = total + a + b;
  }
  return total;
}
//# sourceMappingURL=./index.js.map
