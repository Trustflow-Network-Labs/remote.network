# Remote Network - Web UI Frontend

Vue.js 3 web interface for Remote Network node management.

## Features

- Multi-provider authentication:
  - Ed25519 private key
- Real-time node monitoring
- Peer management
- Workflow execution (coming soon)

## Development

### Prerequisites

- Node.js 18+ and npm

### Setup

```bash
# Install dependencies
npm install

# Start development server
npm run dev
```

The development server will start on `http://localhost:5173` and proxy API calls to the Go backend on port 6060.

### Build

```bash
# Build for production
npm run build
```

The built files will be in the `dist/` directory, which will be served by the Go API server.

## Project Structure

```
src/
├── components/       # Reusable Vue components
│   ├── auth/        # Authentication components
│   ├── dashboard/   # Dashboard components
│   └── common/      # Common UI components
├── views/           # Page components (routed)
├── services/        # API and auth service clients
├── stores/          # Pinia state management
├── router/          # Vue Router configuration
└── main.ts          # Application entry point
```

## Tech Stack

- **Vue 3** - Progressive JavaScript framework
- **TypeScript** - Type safety
- **Vite** - Fast build tool
- **Pinia** - State management
- **Vue Router** - Routing
- **Axios** - HTTP client
