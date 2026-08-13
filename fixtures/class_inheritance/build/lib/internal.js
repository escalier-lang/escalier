export class Animal {
  constructor(temp1) {
    const name = temp1;
    this.name = name;
  }
  speak() {
    return "...";
  }
  describe() {
    return this.speak();
  }
}
export class Dog extends Animal {
  constructor(temp2, temp3) {
    const name = temp2;
    const breed = temp3;
    super(name);
    this.breed = breed;
  }
  speak() {
    return "woof";
  }
}
export class Puppy extends Animal {
  constructor(temp4) {
    const named = temp4;
    let temp5;
    if (named) {
      temp5 = super("rex");
    } else {
      temp5 = super("stray");
    }
    temp5;
  }
  speak() {
    return "yip";
  }
}
export const d = new Dog("rex", "lab");
export const described = d.describe();
export const heard = d.speak();
export const named = d.name;
export const p = new Puppy(true);
export const yipped = p.speak();
//# sourceMappingURL=./internal.js.map
