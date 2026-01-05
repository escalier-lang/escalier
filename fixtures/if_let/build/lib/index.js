export const tuple = [42, "hello"];
let temp1;
if (tuple != null && tuple.length == 2) {
  const [num, str] = tuple;
  temp1 = "Number: " + num.toString() + ", String: " + str;
} else {
  temp1 = "No value";
}
export const basicIfLetStr = temp1;
export const tupleNum = [42, "hello"];
let temp2;
if (tupleNum != null && tupleNum.length == 2) {
  const [num, str] = tupleNum;
  temp2 = num * 2;
} else {
  temp2 = 0;
}
export const basicIfLetNum = temp2;
export const nullableTuple = null;
let temp3;
if (nullableTuple != null && nullableTuple.length == 2) {
  const [flag, value] = nullableTuple;
  let temp4;
  if (flag) {
    temp4 = value * 2;
  } else {
    temp4 = value;
  }
  temp3 = temp4;
} else {
  temp3 = -1;
}
export const ifLetWithElseNum = temp3;
let temp5;
if (nullableTuple != null && nullableTuple.length == 2) {
  const [flag, value] = nullableTuple;
  let temp6;
  if (flag) {
    temp6 = "Value: " + value.toString();
  } else {
    temp6 = "Not flagged";
  }
  temp5 = temp6;
} else {
  temp5 = "No tuple";
}
export const ifLetWithElseStr = temp5;
export const nestedNum = [[1, 2], [3, 4]];
let temp7;
if (nestedNum != null && nestedNum.length == 2 && nestedNum[0].length == 2 && nestedNum[1].length == 2) {
  const [[x, y], [a, b]] = nestedNum;
  temp7 = x + y + a + b;
} else {
  temp7 = 0;
}
export const nestedIfLetNum = temp7;
export const nestedStr = [[1, "b"], ["c", "d"]];
let temp8;
if (nestedStr != null && nestedStr.length == 2 && nestedStr[0].length == 2 && nestedStr[1].length == 2) {
  const [[num, str1], [str2, str3]] = nestedStr;
  temp8 = "Num: " + num.toString() + ", Str1: " + str1 + ", Str2: " + str2 + ", Str3: " + str3;
} else {
  temp8 = "none";
}
export const nestedIfLetStr = temp8;
export const point = {x: 10, y: 20};
let temp9;
if (point != null && point != null && "x" in point && "y" in point) {
  const {x, y} = point;
  temp9 = x * y;
} else {
  temp9 = 0;
}
export const objectIfLetNum = temp9;
let temp10;
if (point != null && point != null && "x" in point && "y" in point) {
  const {x, y} = point;
  temp10 = "Point: (" + x.toString() + ", " + y.toString() + ")";
} else {
  temp10 = "No point";
}
export const objectIfLetStr = temp10;
export const config = {enabled: true, count: 5};
let temp11;
if (config != undefined && config != null && "enabled" in config && "count" in config) {
  const {enabled, count} = config;
  let temp12;
  if (enabled) {
    temp12 = count * 2;
  } else {
    temp12 = 0;
  }
  temp11 = temp12;
} else {
  temp11 = -1;
}
export const shorthandIfLetNum = temp11;
let temp13;
if (config != undefined && config != null && "enabled" in config && "count" in config) {
  const {enabled, count} = config;
  let temp14;
  if (enabled) {
    temp14 = "Count: " + count.toString();
  } else {
    temp14 = "Disabled";
  }
  temp13 = temp14;
} else {
  temp13 = "No config";
}
export const shorthandIfLetStr = temp13;
export const option = 42;
let temp15;
if (option != null && true) {
  const valueNum = option;
  temp15 = valueNum * 2;
} else {
  temp15 = 0;
}
export const ifLetWithExprAltNum = temp15;
export const complex = [100, ["test", true]];
let temp16;
if (complex != null && complex.length == 2 && typeof complex[0] == "number" && complex[1].length == 2 && typeof complex[1][0] == "string" && typeof complex[1][1] == "boolean") {
  const [num, [str, flag]] = complex;
  let temp17;
  if (flag) {
    temp17 = num + str.length;
  } else {
    temp17 = num;
  }
  temp16 = temp17;
} else {
  temp16 = 0;
}
export const complexIfLetNum = temp16;
let temp18;
if (complex != null && complex.length == 2 && typeof complex[0] == "number" && complex[1].length == 2 && typeof complex[1][0] == "string" && typeof complex[1][1] == "boolean") {
  const [num, [str, flag]] = complex;
  let temp19;
  if (flag) {
    temp19 = "Number: " + num.toString() + ", Length: " + str.length.toString();
  } else {
    temp19 = "Number: " + num.toString();
  }
  temp18 = temp19;
} else {
  temp18 = "No complex";
}
export const complexIfLetStr = temp18;
export const data = {user: {name: "Alice", age: 30}, active: true};
let temp20;
if (data != null && data != null && "user" in data && data.user != null && "name" in data.user && "age" in data.user && "active" in data) {
  const {user: {name, age}, active} = data;
  let temp21;
  if (active) {
    temp21 = name + " is " + age.toString() + " years old";
  } else {
    temp21 = name;
  }
  temp20 = temp21;
} else {
  temp20 = "Unknown user";
}
export const multipleBindingsStr = temp20;
let temp22;
if (data != null && data != null && "user" in data && data.user != null && "name" in data.user && "age" in data.user && "active" in data) {
  const {user: {name: _, age}, active} = data;
  let temp23;
  if (active) {
    temp23 = age * 2;
  } else {
    temp23 = 0;
  }
  temp22 = temp23;
} else {
  temp22 = 0;
}
export const multipleBindingsNum = temp22;
//# sourceMappingURL=./index.js.map
