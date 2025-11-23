export const fix = function (temp1) {
  const f = temp1;
  const temp3 = {recurse(temp2) {
    const arg = temp2;
    return f(this.recurse.bind(this))(arg);
  }};
  return temp3.recurse.bind(temp3);
};
export const fact = function (temp4) {
  const cont = temp4;
  return function (temp5) {
    const n = temp5;
    let temp6;
    if (n <= 0) {
      temp6 = 1;
    } else {
      temp6 = n * cont(n - 1);
    }
    return temp6;
  };
};
export const result = fix(fact)(10);
//# sourceMappingURL=./index.js.map
