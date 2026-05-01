export class Box {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
  getValue(temp2) {
    const defaultValue = temp2;
    if (this.value != 0) {
      return this.value;
    } else {
      return defaultValue;
    }
  }
}
export const box = new Box(5);
export const a = box.getValue("default");
export const b = box.getValue(10);
//# sourceMappingURL=./index.js.map
