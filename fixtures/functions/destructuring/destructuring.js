type;
Obj  {a: number, b: string, c: boolean;
const foo = function (temp1) {
  const {a, b, ...rest} = temp1;
  return rest;
};
function bar(temp2) {
  const {a, b, ...rest} = temp2;
  return rest;
}
//# sourceMappingURL=./destructuring.esc.map
