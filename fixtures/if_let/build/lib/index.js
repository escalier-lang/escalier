export const tuple = [42, "hello"];
let temp1;
if (tuple != null && tuple.length == 2) {
  const [num, str] = tuple;
  temp1 = "Number: " + num.toString() + ", String: " + str;
} else {
  temp1 = "No value";
}
export const basicIfLetStr = temp1;
export const complex = [100, ["test", true]];
let temp2;
if (complex != null && complex.length == 2 && typeof complex[0] == "number" && complex[1].length == 2 && typeof complex[1][0] == "string" && typeof complex[1][1] == "boolean") {
  const [num, [str, flag]] = complex;
  let temp3;
  if (flag) {
    temp3 = num + str.length;
  } else {
    temp3 = num;
  }
  temp2 = temp3;
} else {
  temp2 = 0;
}
export const complexIfLetNum = temp2;
export const config = {enabled: true, count: 5};
export const data = {user: {name: "Alice", age: 30}, active: true};
export const nullableTuple = null;
let temp4;
if (nullableTuple != null && nullableTuple.length == 2) {
  const [flag, value] = nullableTuple;
  let temp5;
  if (flag) {
    temp5 = value * 2;
  } else {
    temp5 = value;
  }
  temp4 = temp5;
} else {
  temp4 = -1;
}
export const ifLetWithElseNum = temp4;
export const option = 42;
let temp6;
if (option != null && true) {
  const valueNum = option;
  temp6 = valueNum * 2;
} else {
  temp6 = 0;
}
export const ifLetWithExprAltNum = temp6;
let temp7;
if (data != null && data != null && "user" in data && data.user != null && "name" in data.user && "age" in data.user && "active" in data) {
  const {user: {name, age}, active} = data;
  let temp8;
  if (active) {
    temp8 = name + " is " + age.toString() + " years old";
  } else {
    temp8 = name;
  }
  temp7 = temp8;
} else {
  temp7 = "Unknown user";
}
export const multipleBindingsStr = temp7;
export const nestedNum = [[1, 2], [3, 4]];
let temp9;
if (nestedNum != null && nestedNum.length == 2 && nestedNum[0].length == 2 && nestedNum[1].length == 2) {
  const [[x, y], [a, b]] = nestedNum;
  temp9 = x + y + a + b;
} else {
  temp9 = 0;
}
export const nestedIfLetNum = temp9;
export const point = {x: 10, y: 20};
let temp10;
if (point != null && point != null && "x" in point && "y" in point) {
  const {x, y} = point;
  temp10 = x * y;
} else {
  temp10 = 0;
}
export const objectIfLetNum = temp10;
let temp11;
if (target != null && target.length == 2) {
  const [num, str] = target;
  temp11 = str;
}
export const result = temp11;
let temp12;
if (config != undefined && config != null && "enabled" in config && "count" in config) {
  const {enabled, count} = config;
  let temp13;
  if (enabled) {
    temp13 = count * 2;
  } else {
    temp13 = 0;
  }
  temp12 = temp13;
} else {
  temp12 = -1;
}
export const shorthandIfLetNum = temp12;
//# sourceMappingURL=./index.js.map
