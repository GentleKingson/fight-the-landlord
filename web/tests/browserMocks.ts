import { vi } from 'vitest';

// jsdom has no layout engine; geometry and ResizeObserver are covered in Playwright.
vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
