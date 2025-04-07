const foo = function (temp1) {
  const {a, b, ...rest} = temp1;
  return rest.length;
};
function bar(temp2) {
  const {a, b, ...rest} = temp2;
  return rest.length;
}
//# sourceMappingURL=./destructuring.esc.map
