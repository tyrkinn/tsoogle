export declare const add: (x: number, y: number) => number


export function filter<T>(pred: (v: T) => boolean, lst: T[]): T[] {
  return lst.filter(pred)
}

export function map<A, B>(fn: (x: A) => B, list: readonly A[]): B[];
