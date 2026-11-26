export class Point {
  constructor(temp1) {
    const x = temp1;
    this.x = x;
  }
}
export function getX(temp2) {
  const {x} = temp2;
  return x;
}
export const {x} = new Point(1);
export const px = x;
//# sourceMappingURL=./index.js.map
