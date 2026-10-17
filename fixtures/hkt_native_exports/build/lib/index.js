export class Box {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
}
export const boxFunctor = {map: function (temp2, temp3) {
  const fa = temp2;
  const f = temp3;
  return new Box(f(fa.value));
}};
export function double(temp4, temp5) {
  const functor = temp4;
  const fa = temp5;
  return functor.map(fa, function (temp6) {
    const n = temp6;
    return n * 2;
  });
}
//# sourceMappingURL=./index.js.map
