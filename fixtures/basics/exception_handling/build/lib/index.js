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
export function throwTypeIsWrong(temp9) {
  const value = temp9;
  let temp10;
  if (value != "") {
    temp10 = value;
  } else {
    let temp11;
    throw Error("value is empty");
    temp10 = temp11;
  }
  temp10;
}
//# sourceMappingURL=./index.js.map
