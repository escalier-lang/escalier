export class Fix {
  constructor(temp1) {
    const f = temp1;
    this.f = f;
  }
  recurse(temp2) {
    const arg = temp2;
    const g = this.f;
    return g(this.recurse.bind(this))(arg);
  }
}
export const fact = function (temp3) {
  const cont = temp3;
  return function (temp4) {
    const n = temp4;
    let temp5;
    if (n <= 0) {
      temp5 = 1;
    } else {
      temp5 = n * cont(n - 1);
    }
    return temp5;
  };
};
export const fix = function (temp6) {
  const f = temp6;
  const temp7 = new Fix(f);
  return temp7.recurse.bind(temp7);
};
export const result = fix(fact)(10);
//# sourceMappingURL=./index.js.map
