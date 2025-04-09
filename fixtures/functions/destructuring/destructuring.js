const foo = function (temp1) {
  const {a, b, ...rest} = temp1;
  return rest.c;
};
function bar(temp2) {
  const {a, b, ...rest} = temp2;
  return rest.c;
}
//# sourceMappingURL=./destructuring.esc.map
