let temp1;
try {
  temp1 = "success";
} catch (__error) {
  if (true) {
    temp1 = "error";
  }
}
export const basicTryCatch = temp1;
let temp2;
try {
  let temp3;
  throw "error";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp2 = "caught: " + msg;
  } else {
    throw __error;
  }
}
export const tryCatchWithThrow = temp2;
let temp4;
try {
  let temp5;
  throw Error("fail");
} catch (__error) {
  if (true) {
    const Error = __error;
    temp4 = "caught error";
  } else {
    temp4 = "unknown";
  }
}
export const multipleCases = temp4;
let temp6;
try {
  let temp7;
  throw "fail";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp6 = msg;
  } else {
    temp6 = "unknown";
  }
}
export const patternBinding = temp6;
let temp8;
try {
  let temp9;
  throw "critical";
} catch (__error) {
  if (true) {
    const err = __error;
    if (err == "critical") {
      temp8 = "critical error";
    } else if (true) {
      temp8 = "other error";
    }
  }
}
export const withGuard = temp8;
let temp10;
try {
  temp10 = 42;
}
export const tryOnly = temp10;
export function safeDivide(temp11, temp12) {
  const a = temp11;
  const b = temp12;
  let temp13;
  try {
    temp13 = a / b;
  } catch (__error) {
    if (true) {
      temp13 = 0;
    }
  }
  return temp13;
}
let temp14;
try {
  let temp15;
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp14 = msg;
  } else {
    temp14 = "unknown";
  }
}
export const objectPattern = temp14;
let temp16;
try {
  let temp17;
  try {
    temp17 = 5;
  } catch (__error) {
    if (true) {
      temp17 = 10;
    }
  }
  temp16 = temp17;
} catch (__error) {
  if (true) {
    temp16 = 0;
  }
}
export const nestedTryCatch = temp16;
let temp18;
try {
  const x = 5;
  temp18 = x + 10;
} catch (__error) {
  if (true) {
    const y = 0;
    temp18 = y;
  }
}
export const blockBody = temp18;
let temp19;
try {
  temp19 = 42;
} catch (__error) {
  if (true) {
    temp19 = "error";
  }
}
export const mixedReturn = temp19;
//# sourceMappingURL=./index.js.map
