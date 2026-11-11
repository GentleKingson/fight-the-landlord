import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { seedDemoState } from './stores/demoState';
import './styles/reset.css';
import './styles/tokens.css';
import './styles/app.css';
import './styles/endGame.css';

// Seed before the first render so demo pages request only their active scene.
const demo = new URLSearchParams(window.location.search).get('demo');
if (demo) seedDemoState(demo);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App demo={demo} />
  </StrictMode>
);
