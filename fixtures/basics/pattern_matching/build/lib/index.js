const num1 = 5;
let temp1;
let temp2;
temp2 = num1;
if (temp2 == 1) {
  temp1 = "one";
} else if (temp2 == 2) {
  temp1 = "two";
} else if (temp2 == 5) {
  temp1 = "five";
} else {
  temp1 = "other";
}
export const literalMatch = temp1;
let num2 = 42;
let temp3;
let temp4;
temp4 = num2;
if (true) {
  const x = temp4;
  temp3 = x * 2;
}
export const variableMatch = temp3;
export const tupleValue = [1, 2, 3];
let temp5;
let temp6;
temp6 = tupleValue;
if (temp6.length == 3) {
  const [a, b, c] = temp6;
  temp5 = a + b + c;
} else {
  temp5 = 0;
}
export const tupleMatch = temp5;
export const objectValue = {x: 10, y: 20};
let temp7;
let temp8;
temp8 = objectValue;
if (temp8 != null && "x" in temp8 && "y" in temp8) {
  const {x, y} = temp8;
  temp7 = x + y;
} else {
  temp7 = 0;
}
export const objectMatch = temp7;
let temp9;
let temp10;
temp10 = objectValue;
if (temp10 != null && "x" in temp10 && "y" in temp10) {
  const {x: a, y: b} = temp10;
  temp9 = a * b;
} else {
  temp9 = 0;
}
export const objectRename = temp9;
export const nestedValue = {point: [1, 2]};
let temp11;
let temp12;
temp12 = nestedValue;
if (temp12 != null && "point" in temp12 && temp12.point.length == 2) {
  const {point: [x, y]} = temp12;
  temp11 = x + y;
} else {
  temp11 = 0;
}
export const nestedMatch = temp11;
const tuple2 = [3, 3];
let temp13;
let temp14;
temp14 = tuple2;
if (temp14.length == 2 && a == b) {
  const [a, b] = temp14;
  temp13 = "equal";
} else if (temp14.length == 2) {
  const [a, b] = temp14;
  temp13 = "not equal";
}
export const guardMatch = temp13;
const str = "hello";
let temp15;
let temp16;
temp16 = str;
if (temp16 == "hi") {
  temp15 = "greeting";
} else if (temp16 == "hello") {
  temp15 = "salutation";
} else if (temp16 == "bye") {
  temp15 = "farewell";
} else {
  temp15 = "unknown";
}
export const multiCase = temp15;
const bool = true;
let temp17;
let temp18;
temp18 = bool;
if (temp18 == true) {
  temp17 = "yes";
} else if (temp18 == false) {
  temp17 = "no";
}
export const boolMatch = temp17;
const num3 = 7;
let temp19;
let temp20;
temp20 = num3;
if (temp20 == 1) {
  temp19 = "one";
} else if (temp20 == 2) {
  temp19 = "two";
} else {
  const n = temp20;
  temp19 = "number: " + n.toString();
}
export const mixedMatch = temp19;
export const longTuple = [1, 2, 3];
let temp21;
let temp22;
temp22 = longTuple;
if (temp22.length == 2) {
  const [first, ...rest] = temp22;
  temp21 = rest;
} else {
  temp21 = 0;
}
export const tupleRestMatch = temp21;
export const extendedPoint = {x: 1, y: 2, z: 3, name: "point"};
let temp23;
let temp24;
temp24 = extendedPoint;
if (temp24 != null && "x" in temp24 && "y" in temp24) {
  const {x, y, ...rest} = temp24;
  temp23 = rest;
} else {
  temp23 = 0;
}
export const objectRestMatch = temp23;
let temp25;
let temp26;
temp26 = ref;
if (temp26 != null && "value" in temp26 && temp26.value == "string") {
  const {value: a} = temp26;
  temp25 = "string";
} else if (temp26 != null && "value" in temp26 && temp26.value == "number") {
  const {value: b} = temp26;
  temp25 = "number";
} else if (temp26 != null && "value" in temp26 && temp26.value == "boolean") {
  const {value: c} = temp26;
  temp25 = "boolean";
}
export const refMatch = temp25;
class Color {
  constructor(temp27, temp28, temp29) {
    const r = temp27;
    const g = temp28;
    const b = temp29;
    this.r = r;
    this.g = g;
    this.b = b;
  }
}
class Event {
  constructor(temp30) {
    const kind = temp30;
    this.kind = kind;
  }
}
let temp31;
let temp32;
temp32 = obj;
if (temp32 instanceof Color && temp32 != null && "r" in temp32 && "g" in temp32 && "b" in temp32) {
  const {r, g, b} = temp32;
  temp31 = r + g + b;
} else if (temp32 instanceof Event && temp32 != null && "kind" in temp32) {
  const {kind} = temp32;
  temp31 = kind;
}
const result = temp31;
//# sourceMappingURL=./index.js.map
