/// <reference types="vite/client" />
import 'react';

declare module '*.proto?raw' {
  const source: string;
  export default source;
}

declare module 'react' {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface ButtonHTMLAttributes<T> {
    command?: string;
    commandfor?: string;
  }
}
