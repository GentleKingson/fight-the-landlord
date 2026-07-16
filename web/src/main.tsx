import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import './styles/reset.css';
import './styles/tokens.css';
import './styles/app.css';
import './styles/lobby.css';
import './styles/table.css';
import './styles/cards.css';
import './styles/drawer.css';
import './styles/result.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
