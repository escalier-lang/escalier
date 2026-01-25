export const fact = function (temp1) {
  const cont = temp1;
  return function (temp2) {
    const n = temp2;
    let temp3;
    if (n <= 0) {
      temp3 = 1;
    } else {
      temp3 = n * cont(n - 1);
    }
    return temp3;
  };
};
export const fix = function (temp4) {
  const f = temp4;
  const temp6 = {recurse(temp5) {
    const arg = temp5;
    return f(this.recurse.bind(this))(arg);
  }};
  return temp6.recurse.bind(temp6);
};
export const result = fix(fact)(10);
//# sourceMappingURL=./index.js.map
