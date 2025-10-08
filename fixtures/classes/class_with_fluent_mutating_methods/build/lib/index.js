class Point {
  constructor(temp4, temp5) {
    const x = temp4;
    const y = temp5;
    this.x = x;
    this.y = y;
  }
  scale(temp1) {
    const factor = temp1;
    this.x = this.x * factor;
    this.y = this.y * factor;
    return this;
  }
  translate(temp2, temp3) {
    const dx = temp2;
    const dy = temp3;
    this.x = this.x + dx;
    this.y = this.y + dy;
    return this;
  }
}
const p = new Point(5, 10);
const q = p.scale(2).translate(1, -1);
//# sourceMappingURL=./index.js.map
