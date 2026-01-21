throw "Something went wrong";
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
export const throwingFunc = function (temp3) {
  const condition = temp3;
  if (condition) {
    return "success";
  } else {
    throw Error("failure");
  }
};
export const multipleThrows = function (temp4) {
  const flag = temp4;
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
export function throwTypeIsWrong(temp5) {
  const value = temp5;
  let temp6;
  if (value != "") {
    temp6 = value;
  } else {
    throw Error("value is empty");
  }
  temp6;
}
//# sourceMappingURL=./index.js.map
