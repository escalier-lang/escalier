class Person {
  constructor(temp2, temp3) {
    const firstName = temp2;
    const lastName = temp3;
    this.firstName = firstName;
    this.lastName = lastName;
  }
  get fullName() {
    return this.firstName + " " + this.lastName;
  }
  set fullName(temp1) {
    const value = temp1;
    const parts = split(value, " ");
    this.firstName = parts[0];
    this.lastName = parts[1];
  }
}
const person = Person("John", "Doe");
const name = person.fullName;
function main() {
  person.fullName = "Jane Smith";
}
//# sourceMappingURL=./index.js.map
