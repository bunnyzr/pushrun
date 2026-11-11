import { lazy, Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router";
import { useTranslation } from "react-i18next";
import "@/i18n";
import AppLayout from "@/components/layout/AppLayout";
import TokenGate from "@/components/TokenGate";
import Toaster from "@/components/Toaster";

// The editor pages carry CodeMirror, so each one is its own chunk. The shell
// and the token gate stay small enough to render on the first paint.
const ProjectsPage = lazy(() => import("@/pages/projects/ProjectsPage"));
const InstancesPage = lazy(() => import("@/pages/instances/InstancesPage"));
const ProvidersPage = lazy(() => import("@/pages/providers/ProvidersPage"));

function PageFallback() {
  const { t } = useTranslation();
  return (
    <div className="grid min-h-[240px] place-items-center text-sm text-muted-foreground">
      {t("common.loading")}
    </div>
  );
}

function page(node: ReactNode) {
  return <Suspense fallback={<PageFallback />}>{node}</Suspense>;
}

const router = createBrowserRouter([
  {
    element: (
      <TokenGate>
        <AppLayout />
      </TokenGate>
    ),
    children: [
      { path: "/", element: <Navigate to="/projects" replace /> },
      { path: "/projects", element: page(<ProjectsPage />) },
      { path: "/instances", element: page(<InstancesPage />) },
      { path: "/providers", element: page(<ProvidersPage />) },
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
