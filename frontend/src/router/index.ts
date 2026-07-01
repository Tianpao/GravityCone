import { createRouter, createWebHashHistory } from "vue-router";

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    {
      path: "/",
      name: "home",
      component: () => import("@/views/HomeView.vue"),
    },
    {
      path: "/settings",
      name: "settings",
      component: () => import("@/views/SettingsView.vue"),
    },
    {
      path: "/user",
      name: "user",
      component: () => import("@/views/UserView.vue"),
    },
    {
      path: "/host-room",
      name: "host-room",
      component: () => import("@/views/HostRoomView.vue"),
    },
    {
      path: "/joined-room",
      name: "joined-room",
      component: () => import("@/views/JoinedRoomView.vue"),
    },
  ],
});

export default router;
