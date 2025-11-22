declare type Pick<T, K extends keyof T> = {[P in K]: T[P]};
