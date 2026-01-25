throw {message: "Custom error", code: 500};
export function getValueOrThrow(temp1) {
  const value = temp1;
  let temp2;
  if (value != "") {
    temp2 = value;
  } else {
    throw Error("value is empty");
  }
  return temp2;
}
export const multipleThrows = function (temp3) {
  const flag = temp3;
  if (flag) {
    throw "string error";
  } else {
    throw 42;
  }
};
export const nestedThrows = function () {
  const innerFunc = function () {
    throw "inner error";
  };
  throw "outer error";
};
throw "Something went wrong";
export function throwTypeIsWrong(temp4) {
  const value = temp4;
  let temp5;
  if (value != "") {
    temp5 = value;
  } else {
    throw Error("value is empty");
  }
  temp5;
}
export const throwingFunc = function (temp6) {
  const condition = temp6;
  if (condition) {
    return "success";
  } else {
    throw Error("failure");
  }
};
//# sourceMappingURL=./index.js.map
