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
    super();
    const name = temp2;
    const breed = temp3;
    this.name = name;
    this.breed = breed;
  }
  speak() {
    return "woof";
  }
}
export const d = new Dog("rex", "lab");
export const described = d.describe();
export const heard = d.speak();
export const named = d.name;
//# sourceMappingURL=./index.js.map
