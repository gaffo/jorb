package org.gaffo.jorb.pico;

import org.gaffo.jorb.IContext;
import org.picocontainer.DefaultPicoContainer;

public class PicoContainerContext implements IContext
{

    private final DefaultPicoContainer pico;

    public PicoContainerContext()
    {
        pico = new DefaultPicoContainer();
    }

    @Override
    public <T> T get(Class<T> clazz)
    {
        return pico.getComponent(clazz);
    }

    @Override
    public void register(Object obj)
    {
        pico.addComponent(obj);
    }
}
