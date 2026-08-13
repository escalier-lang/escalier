export const geo = {};
class geo__Point {
  constructor(temp1, temp2) {
    const x = temp1;
    const y = temp2;
    this.x = x;
    this.y = y;
  }
}
geo.Point = geo__Point;
function geo__makeOrigin() {
  return new geo__Point(0, 0);
}
geo.makeOrigin = geo__makeOrigin;
//# sourceMappingURL=./index.js.map
