import {
  useRef,
  type CompositionEventHandler,
  type FocusEventHandler,
  type KeyboardEvent,
  type KeyboardEventHandler
} from 'react';

export function isImeComposing(
  event: KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>,
  composing = false
): boolean {
  return composing || event.nativeEvent.isComposing || event.keyCode === 229 || event.nativeEvent.keyCode === 229;
}

export function useImeSafeEnter(): {
  onCompositionStart: CompositionEventHandler<HTMLInputElement>;
  onCompositionEnd: CompositionEventHandler<HTMLInputElement>;
  onBlur: FocusEventHandler<HTMLInputElement>;
  onKeyDown: KeyboardEventHandler<HTMLInputElement>;
} {
  const composingRef = useRef(false);

  return {
    onCompositionStart: () => {
      composingRef.current = true;
    },
    onCompositionEnd: () => {
      composingRef.current = false;
    },
    onBlur: () => {
      composingRef.current = false;
    },
    onKeyDown: (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      if (isImeComposing(event, composingRef.current)) return;
      event.currentTarget.form?.requestSubmit();
    }
  };
}
