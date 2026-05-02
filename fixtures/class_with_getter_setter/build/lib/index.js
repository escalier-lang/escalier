export class Person {
  constructor(temp1, temp2) {
    const firstName = temp1;
    const lastName = temp2;
    this.firstName = firstName;
    this.lastName = lastName;
  }
  get fullName() {
    return this.firstName + " " + this.lastName;
  }
  set fullName(temp3) {
    const value = temp3;
    const parts = split(value, " ");
    this.firstName = parts[0];
    this.lastName = parts[1];
  }
}
export const person = new Person("John", "Doe");
export function main() {
  person.fullName = "Jane Smith";
}
export const name = person.fullName;
//# sourceMappingURL=./index.js.map
