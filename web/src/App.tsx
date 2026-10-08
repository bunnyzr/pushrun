import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router";
import "@/i18n";
import AppLayout from "@/components/layout/AppLayout";
import TokenGate from "@/components/TokenGate";
import Toaster from "@/components/Toaster";
import InstancesPage from "@/pages/instances/InstancesPage";
import ProjectsPage from "@/pages/projects/ProjectsPage";
import ProvidersPage from "@/pages/providers/ProvidersPage";

const router = createBrowserRouter([
  {
    element: (
      <TokenGate>
        <AppLayout />
      </TokenGate>
    ),
    children: [
      { path: "/", element: <Navigate to="/projects" replace /> },
      { path: "/projects", element: <ProjectsPage /> },
      { path: "/instances", element: <InstancesPage /> },
      { path: "/providers", element: <ProvidersPage /> },
    ],
  },
]);

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>
  );
}
