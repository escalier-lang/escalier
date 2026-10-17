export class Base {
  constructor(temp1) {
    const x = temp1;
    this.x = x;
  }
}
export class Seeded {
  constructor(temp2) {
    const seed = temp2;
    this.x = 1;
  }
}
export class Derived extends Base {
  constructor(...temp3) {
    const args = temp3;
    super(...args.slice(0, args.length - 1));
    this.y = args[args.length - 1];
  }
}
export class FromSeed extends Seeded {
  constructor(...temp4) {
    const args = temp4;
    super(...args.slice(0, args.length - 1));
    this.y = args[args.length - 1];
  }
}
export class Leaf extends Derived {
  constructor(...temp5) {
    const args = temp5;
    super(...args.slice(0, args.length - 1));
    this.z = args[args.length - 1];
  }
}
export const d = new Derived(1, 2);
export const leaf = new Leaf(1, 2, "three");
//# sourceMappingURL=./index.js.map
