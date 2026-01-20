export async function fetchData(temp1) {
  const url = temp1;
  return "data from " + url;
}
export async function fetchWithAwait(temp2) {
  const url = temp2;
  const data = await fetch(url);
  return data;
}
export async function fetchWithError(temp3) {
  const shouldError = temp3;
  let temp4;
  if (shouldError) {
    throw "Something went wrong";
  }
  temp4;
  return "success";
}
export async function fetchMultiple(temp5, temp6) {
  const url1 = temp5;
  const url2 = temp6;
  const data1 = await fetch(url1);
  const text1 = await data1.text();
  const data2 = await fetch(url2);
  const text2 = await data2.text();
  return text1 + " and " + text2;
}
export async function conditionalFetch(temp7, temp8) {
  const useCache = temp7;
  const url = temp8;
  if (useCache) {
    return "cached data";
  } else {
    const data = await fetch(url);
    return data;
  }
}
//# sourceMappingURL=./index.js.map
