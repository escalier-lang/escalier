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
export const throwingFunc = function (temp4) {
  const condition = temp4;
  if (condition) {
    return "success";
  } else {
    throw Error("failure");
  }
};
//# sourceMappingURL=./index.js.map
