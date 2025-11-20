export const i = 2;
export const j = 3;
export const coord1 = "y";
export const coord2 = "z";
export class Event {
  constructor(temp1) {
    const msg = temp1;
    this.msg = msg;
  }
  print() {
    console.log(this.msg);
  }
}
export const event = new Event("click");
export const key1 = "msg";
export const key2 = "print";
//# sourceMappingURL=./index.js.map
