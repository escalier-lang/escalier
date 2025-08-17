let temp1;
throw "Something went wrong";
export const simpleThrow = temp1;
let temp2;
throw {message: "Custom error", code: 500};
export const errorThrow = temp2;
export function getValueOrThrow(temp3) {
  const value = temp3;
  let temp4;
  if (value != "") {
    temp4 = value;
  } else {
    let temp5;
    throw Error("value is empty");
    temp4 = temp5;
  }
  return temp4;
}
export const throwingFunc = function (temp6) {
  const condition = temp6;
  let temp7;
  if (condition) {
    return "success";
    temp7 = undefined;
  } else {
    let temp8;
    throw Error("failure");
    return temp8;
    temp7 = undefined;
  }
  temp7;
};
export const multipleThrows = function (temp9) {
  const flag = temp9;
  let temp10;
  if (flag) {
    let temp11;
    throw "string error";
    temp10 = temp11;
  } else {
    let temp12;
    throw 42;
    temp10 = temp12;
  }
  temp10;
};
export const nestedThrows = function () {
  const innerFunc = function () {
    let temp13;
    throw "inner error";
    temp13;
  };
  let temp14;
  throw "outer error";
  temp14;
};
export function throwTypeIsWrong(temp15) {
  const value = temp15;
  let temp16;
  if (value != "") {
    temp16 = value;
  } else {
    let temp17;
    throw Error("value is empty");
    temp16 = temp17;
  }
  temp16;
}
//# sourceMappingURL=./index.js.map
