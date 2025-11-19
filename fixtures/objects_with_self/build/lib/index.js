export const value = 5;
export const obj1 = {value, increment(temp1) {
  const amount = temp1;
  this.value = this.value + amount;
  return this;
}};
export const inc = obj1.increment.bind(obj1);
export const obj2 = {_value: value, get value() {
  return this._value;
}, set value(temp2) {
  const value = temp2;
  this._value = value;
}};
//# sourceMappingURL=./index.js.map
