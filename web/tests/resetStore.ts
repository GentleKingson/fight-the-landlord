import { beforeEach } from 'vitest';
import { useAppStore } from '../src/stores/appStore';

beforeEach(() => {
  sessionStorage.clear();
  useAppStore.setState(useAppStore.getInitialState(), true);
});
