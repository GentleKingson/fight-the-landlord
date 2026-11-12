import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { seedDemoState } from './stores/demoState';
import './styles/reset.css';
import './styles/tokens.css';
import './styles/app.css';
import './styles/cards.css';
import './styles/lobby.css';
import './styles/table.css';
import './styles/playedCards.css';
import './styles/hand.css';
import './styles/dealAnimation.css';
import './styles/utilityDrawer.css';
import './styles/result.css';
import './styles/endGame.css';
import './styles/theme.css';

// Seed before the first render so demo pages request only their active scene.
const demo = new URLSearchParams(window.location.search).get('demo');
if (demo) seedDemoState(demo);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App demo={demo} />
  </StrictMode>
);
