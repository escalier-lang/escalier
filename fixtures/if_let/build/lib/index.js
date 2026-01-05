export const tuple = [42, "hello"];
let temp1;
if (tuple != null && tuple.length == 2) {
  const [num, str] = tuple;
  temp1 = "Number: " + num.toString() + ", String: " + str;
} else {
  temp1 = "No value";
}
export const basicIfLetStr = temp1;
export const nullableTuple = null;
let temp2;
if (nullableTuple != null && nullableTuple.length == 2) {
  const [flag, value] = nullableTuple;
  let temp3;
  if (flag) {
    temp3 = value * 2;
  } else {
    temp3 = value;
  }
  temp2 = temp3;
} else {
  temp2 = -1;
}
export const ifLetWithElseNum = temp2;
export const nestedNum = [[1, 2], [3, 4]];
let temp4;
if (nestedNum != null && nestedNum.length == 2 && nestedNum[0].length == 2 && nestedNum[1].length == 2) {
  const [[x, y], [a, b]] = nestedNum;
  temp4 = x + y + a + b;
} else {
  temp4 = 0;
}
export const nestedIfLetNum = temp4;
export const point = {x: 10, y: 20};
let temp5;
if (point != null && point != null && "x" in point && "y" in point) {
  const {x, y} = point;
  temp5 = x * y;
} else {
  temp5 = 0;
}
export const objectIfLetNum = temp5;
export const config = {enabled: true, count: 5};
let temp6;
if (config != undefined && config != null && "enabled" in config && "count" in config) {
  const {enabled, count} = config;
  let temp7;
  if (enabled) {
    temp7 = count * 2;
  } else {
    temp7 = 0;
  }
  temp6 = temp7;
} else {
  temp6 = -1;
}
export const shorthandIfLetNum = temp6;
export const option = 42;
let temp8;
if (option != null && true) {
  const valueNum = option;
  temp8 = valueNum * 2;
} else {
  temp8 = 0;
}
export const ifLetWithExprAltNum = temp8;
export const complex = [100, ["test", true]];
let temp9;
if (complex != null && complex.length == 2 && typeof complex[0] == "number" && complex[1].length == 2 && typeof complex[1][0] == "string" && typeof complex[1][1] == "boolean") {
  const [num, [str, flag]] = complex;
  let temp10;
  if (flag) {
    temp10 = num + str.length;
  } else {
    temp10 = num;
  }
  temp9 = temp10;
} else {
  temp9 = 0;
}
export const complexIfLetNum = temp9;
export const data = {user: {name: "Alice", age: 30}, active: true};
let temp11;
if (data != null && data != null && "user" in data && data.user != null && "name" in data.user && "age" in data.user && "active" in data) {
  const {user: {name, age}, active} = data;
  let temp12;
  if (active) {
    temp12 = name + " is " + age.toString() + " years old";
  } else {
    temp12 = name;
  }
  temp11 = temp12;
} else {
  temp11 = "Unknown user";
}
export const multipleBindingsStr = temp11;
//# sourceMappingURL=./index.js.map
