package org.gaffo.jorb.annotations;

/**
 * Denotes a task that is going out to the net to pull down data
 *
 * The idea is that we can seperate things onto fetch queues or job them to different
 * workers on different hosts if they are fetch tasks versus computational tasks, thus
 * allowing us to throttle our crawling
 */
public @interface Category
{
    public String name();
}
