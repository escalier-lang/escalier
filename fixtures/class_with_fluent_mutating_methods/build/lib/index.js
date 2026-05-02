export class Point {
  constructor(temp1, temp2) {
    const x = temp1;
    const y = temp2;
    this.x = x;
    this.y = y;
  }
  scale(temp3) {
    const factor = temp3;
    this.x = this.x * factor;
    this.y = this.y * factor;
    return this;
  }
  translate(temp4, temp5) {
    const dx = temp4;
    const dy = temp5;
    this.x = this.x + dx;
    this.y = this.y + dy;
    return this;
  }
}
export const p = new Point(5, 10);
export const q = p.scale(2).translate(1, -1);
//# sourceMappingURL=./index.js.map
