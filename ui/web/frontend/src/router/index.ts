import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/login'
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('../components/auth/LoginView.vue')
  },
  {
    path: '/dashboard',
    name: 'Dashboard',
    component: () => import('../components/dashboard/DashboardView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/peers',
    name: 'Peers',
    component: () => import('../components/peers/PeersView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/services',
    name: 'Services',
    component: () => import('../components/services/ServicesView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/services/add',
    name: 'AddLocalService',
    component: () => import('../views/AddLocalServicePage.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/services/peer/:peerId',
    name: 'PeerServices',
    component: () => import('../components/services/ServicesView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/workflows',
    name: 'Workflows',
    component: () => import('../components/workflows/WorkflowsView.vue'),
    meta: { requiresAuth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

// Navigation guard for authentication
router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()

  if (to.meta.requiresAuth && !authStore.isAuthenticated) {
    next('/login')
  } else if (to.path === '/login' && authStore.isAuthenticated) {
    next('/dashboard')
  } else {
    next()
  }
})

export default router
